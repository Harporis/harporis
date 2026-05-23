# Harporis Getter — Design (MVP)

Status: draft / awaiting review
Date: 2026-05-23
Scope: MVP getter service of the Harporis git secrets scanner

## 0. Контекст

Harporis — система поиска секретов в git-репозиториях. MVP состоит из трёх сервисов и шины:

```
CLI / API gateway --gRPC--> Getter ---NATS JetStream---> Validator(s) ---NATS---> Writer(s)
```

- **Getter** — нормализует данные из git в типизированные структуры, публикует в NATS
- **Validator** — пуллит чанки, прогоняет regex/entropy правила, публикует findings
- **Writer** — пуллит findings, персистит (формат вывода конфигурируем per-scan: append-file, SQL, etc)

Все три тира масштабируются горизонтально (несколько инстансов на тир). Корреляция через `scan_id` (UUID, end-to-end).

Этот документ покрывает **только Getter**. Writer-формат, валидатор-логика правил — out of scope.

## 1. Роль и границы Getter

**Делает:**
- Принимает `ScanRequest` через gRPC
- Если источник — remote URL, клонирует репо во временную директорию (HTTPS+PAT или SSH через ssh-agent)
- Если источник — локальный путь, использует как есть (read-only)
- Перечисляет источники данных согласно `ScanType` (commits, blobs, diff hunks)
- Дедупит по `blob_sha` для history-сканов
- Парсит блобы построчно, режет на чанки `GitRowChunk` с overlap
- Публикует чанки в NATS JetStream
- Публикует события статуса в NATS

**Не делает:**
- Не знает о правилах детекта (нет regex)
- Не персистит findings
- Не хранит состояние между сканами (stateless, scan-scoped)

## 2. Архитектура и поток данных

```
       CLI / API gateway
              │
              │  gRPC StartScan(ScanRequest)
              ▼
        ┌──────────┐
        │  Getter  │
        │ instance │
        └────┬─────┘
             │
             │ Per scan_id:
             │  1× Git walker goroutine
             │  N× Worker goroutines (own `git cat-file --batch` subprocess each)
             │
             │ NATS publish
             ▼
       JetStream subjects:
         harporis.chunks.<scan_id>    (data)
         harporis.status.<scan_id>    (control events)
         harporis.findings.<scan_id>  (от валидатора, геттер сюда не пишет)
```

### Per-scan goroutines в одном инстансе геттера

```
┌──────────────────────┐
│ Git walker (1)       │  rev-list / ls-tree, dedup blob_sha,
│                      │  shipping jobs in jobs chan
└──────────┬───────────┘
           │  jobs chan (cap=2*N)
           ▼
┌──────────┴───────────┐  ┌──────────────────────┐
│ Worker 1             │  │ Worker N             │   each owns its
│                      │  │                      │   own `git cat-file
│ git cat-file --batch │..│ git cat-file --batch │   --batch` subprocess
│ bufio.Scanner        │  │                      │
│ chunk builder        │  │                      │
│ NATS publish (block) │  │                      │
└──────────────────────┘  └──────────────────────┘
```

**Горизонтальное масштабирование:** несколько getter-инстансов обслуживают разные `scan_id`. Внутри одного скана для MVP — единственный walker (не делим коммит-диапазон между walker'ами; revisit если профайл покажет узкое горло).

## 3. ScanRequest и типы скана

```proto
message ScanRequest {
  string         scan_id   = 1;  // UUID; ALREADY_EXISTS если уже активен
  Source         source    = 2;
  ScanType       type      = 3;
  ScanRange      range     = 4;
  ConfigOverride overrides = 5;  // allowlist полей из getter.yaml
  OutputConfig   output    = 6;  // для writer'а; геттер пробрасывает в status event
}

// OutputConfig — opaque для геттера, схема определяется writer'ом
// (file path / SQL DSN / S3 bucket / etc). Геттер только пробрасывает.
message OutputConfig {
  string kind = 1;                       // "file" | "sql" | "s3" | ...
  map<string, string> params = 2;        // зависит от kind
}

message Source {
  oneof src {
    string     local_path = 1;
    RemoteRepo remote     = 2;
  }
}

message RemoteRepo {
  string url = 1;
  oneof auth {
    string  token = 2;   // PAT для HTTPS
    SshAuth ssh   = 3;   // или используем ssh-agent
  }
}

message ScanRange {
  string branch       = 1;  // BRANCH_FULL, BRANCH_DIFF (head)
  string base_branch  = 2;  // BRANCH_DIFF (base)
  string commit_from  = 3;  // COMMIT_RANGE
  string commit_to    = 4;  // COMMIT_RANGE
}

enum ScanType {
  SCAN_TYPE_UNSPECIFIED = 0;
  FULL_HISTORY  = 1;
  BRANCH_FULL   = 2;
  COMMIT_RANGE  = 3;
  CURRENT_STATE = 4;
  BRANCH_DIFF   = 5;
  HEAD_DIFF     = 6;
  STAGED        = 7;
}
```

### Mapping ScanType → git-команды → чанк-kind

| ScanType        | Scope                            | Git-команды                                                 | Kind          |
|-----------------|----------------------------------|-------------------------------------------------------------|---------------|
| `CURRENT_STATE` | snapshot HEAD                    | `ls-tree -r -l HEAD` → `cat-file --batch`                  | BLOB          |
| `FULL_HISTORY`  | unique blobs всех refs           | `rev-list --all` + `ls-tree -l` per commit → dedup → batch | BLOB          |
| `BRANCH_FULL`   | unique blobs истории ветки       | `rev-list <branch>` + `ls-tree -l` per commit → dedup      | BLOB          |
| `COMMIT_RANGE`  | unique blobs в A..B              | `rev-list A..B` + `ls-tree -l` per commit → dedup          | BLOB          |
| `BRANCH_DIFF`   | изменённые файлы base..head      | `diff --name-status base..head` + `diff -U30 base..head`   | DIFF_WINDOW   |
| `HEAD_DIFF`     | unstaged                         | `diff --name-status` + `diff -U30`                          | DIFF_WINDOW   |
| `STAGED`        | в индексе                        | `diff --cached --name-status` + `diff --cached -U30`        | DIFF_WINDOW   |

**Размер пред-фильтра:** для BLOB-типов используем `git cat-file --batch-check='%(objectsize)'` чтобы получить размер БЕЗ загрузки контента. Файлы >`max_file_size_mb` skip'аются до `--batch`.

**Removed files в diff-сканах** пропускаем (нечего сканировать в свежем состоянии).

**Дедуп:** только для BLOB-чанков. In-memory `set[blob_sha]` (≈20 байт × N unique blobs). Первая встреча → эмитим все чанки блоба; повторы → skip. `refs[]` в MVP содержит только first-seen `(commit_sha, path)` — для богатых refs writer/UI могут добрать `git log -- <path>` отдельно.

## 4. Data model (контракты `contracts/`)

```proto
syntax = "proto3";
package harporis.v1;

message GitRow {
  int32 line_number = 1;   // 1-based, как в редакторе
  int64 byte_offset = 2;   // байтовый офсет начала строки в blob (для BLOB) или в reconstructed file (для DIFF_WINDOW)
  bytes content     = 3;   // байты строки БЕЗ trailing \n
}

enum ChunkKind {
  CHUNK_KIND_UNSPECIFIED = 0;
  BLOB        = 1;
  DIFF_WINDOW = 2;
}

message GitRowChunk {
  string    scan_id         = 1;
  string    chunk_id        = 2;  // UUID
  int64     sequence_number = 3;  // per-worker monotonic
  bool      is_last_in_scan = 4;
  ChunkKind kind            = 5;

  // BLOB-kind поля
  string                 blob_sha = 10;
  repeated CommitFileRef refs     = 11;  // MVP: 1 запись (first-seen)

  // DIFF_WINDOW-kind поля
  string commit_sha          = 20;
  string file_path           = 21;
  int32  context_lines_above = 22;
  int32  context_lines_below = 23;

  // Слайс одного источника
  int32 start_line  = 30;  // первая строка чанка (1-based)
  int32 end_line    = 31;
  int32 total_lines = 32;  // всего строк в исходном blob/файле
  int32 chunk_index = 33;  // 0-based
  int32 chunk_count = 34;  // всего слайсов для этого источника

  repeated GitRow rows = 40;  // непрерывный диапазон [start_line..end_line]
}

message CommitFileRef {
  string commit_sha = 1;
  string path       = 2;
  int64  timestamp  = 3;
}
```

### Семантика чанка

- `rows[]` — НЕПРЕРЫВНЫЙ диапазон строк одного источника
- Валидатор склеивает их обратно в текст и гонит regex по всему окну → multiline-секреты (PEM, JSON service-account) ловятся целиком
- Если источник не влез в один чанк, режем на несколько с overlap последних M строк (default M=64 ≈ 8 КБ — покрывает RSA PEM ~52 строки, JSON creds ~12 строк, JWT — 1 строка)
- Все чанки одного источника несут одинаковые `blob_sha` (BLOB) или `commit_sha+file_path` (DIFF_WINDOW), плюс `chunk_index/chunk_count`

### Размеры по умолчанию (всё конфигурируемо)

| Параметр              | Default | Обоснование                                                                    |
|-----------------------|---------|--------------------------------------------------------------------------------|
| `row_size_target_kb`  | 256     | ~95% source-файлов влезают в 1 чанк; chunk fits 3-4 chunks в default NATS 1 MB |
| `row_overlap_lines`   | 64      | ~8 КБ; покрывает все известные multiline-секреты                               |
| `max_file_size_mb`    | 10      | Отсекает lockfile/sql-dump мусор; секреты в файлах >10 МБ редки                |
| `diff_context_lines`  | 30      | ±30 строк вокруг хунка; covers PEM-в-хунке                                     |

## 5. Файловый фильтр (5-слойный, early-exit)

Порядок от дешёвого к дорогому:

```go
func shouldScan(path string, size int64) (bool, skipReason) {
    if matchAnyGlob(path, cfg.PathExclusions)            { return false, "path_excluded" }
    ext := strings.ToLower(filepath.Ext(path))
    if _, b := binarySkipExts[ext]; b                    { return false, "binary_extension" }
    if size > cfg.MaxFileSize                            { return false, "size_cap" }
    if gitAttributes.IsBinary(path)                      { return false, "gitattributes_binary" }
    if hasNULByte(readPrefix(blob, 8192))                { return false, "nul_byte" }
    return true, ""
}
```

**Default `path_exclusions`:** `.git/`, `node_modules/`, `vendor/`, `dist/`, `build/`, `target/`, `.next/`, `.venv/`, `__pycache__/`.

**Default `binary_extensions`:**
```
.png .jpg .jpeg .gif .webp .ico .svg .pdf
.zip .gz .tar .bz2 .xz .7z .rar .lz .lzma .zst
.exe .dll .so .dylib .class .jar
.woff .woff2 .ttf .otf .eot
.mp3 .mp4 .mov .avi .flac .wav .mkv .webm .m4a
.docx .xlsx .pptx
.db .sqlite .sqlite3 .mdb
.iso .img .dmg
```

**НЕ skip'аем** (даже если выглядит binary): `.min.js`, `.min.css`, `.lock`, `.ipynb` — секреты там встречаются регулярно.

**`.gitattributes`:** парсим один раз на скан (root + per-subtree). Файлы с `binary` или `-text` атрибутом — skip.

**NUL-byte sniff:** последний слой, требует I/O (первые 8 КБ блоба).

Каждый skip пишет метрику `blobs_skipped{reason}`. Никаких тихих дропов.

## 6. Ресурсные лимиты

| Конфиг          | Реализация                                                                              |
|-----------------|------------------------------------------------------------------------------------------|
| `max_cpu_cores` | `runtime.GOMAXPROCS(n)`. Размер worker-pool на скан = `min(n, default_workers=4)`. n=1 → cooperative scheduling, всё работает медленно. |
| `max_ram_mb`    | `debug.SetMemoryLimit(n*1024*1024)`. Soft limit, агрессивный GC. + `sync.Pool` для буферов строк и `[]GitRow`. + размер `jobs chan` = `max(2, n*1024/256)`. |

### Streaming → bounded peak memory

```
git cat-file --batch  →  bufio.Scanner  →  chunk builder  →  NATS publish
   (pipe stream)         (1 line at         (накопляет N        (блокирует
                          a time, ~64KB      строк, emit         при slow
                          internal buf)      chunk, overlap)     NATS)
```

- Блоб НИКОГДА не лежит в памяти целиком
- Per-blob peak: ~256 КБ chunk + ~8 КБ overlap + ~64 КБ scanner buf = **<400 КБ**
- Память не растёт ни с размером блоба (стримим), ни с числом блобов (последовательно)

### Backpressure
NATS publish медленный → worker встаёт на publish → walker встаёт на полном `jobs chan` → cat-file блокируется на write в полный pipe → git ждёт. Цепочка естественная.

### Удовлетворение требования «работает медленно, но работает»
При `max_cpu_cores=1, max_ram_mb=64` всё стримит через одну OS-thread с тесным GC, медленно но без падений. Только дисковое пространство фундаментально лимитирует — нужно вмещать клон.

## 7. gRPC сервис

```proto
service GetterService {
  rpc StartScan     (ScanRequest)       returns (ScanResponse);
  rpc CancelScan    (CancelScanRequest) returns (CancelScanResponse);
  rpc GetScanStatus (ScanStatusRequest) returns (ScanStatusResponse);
  rpc Health        (HealthRequest)     returns (HealthResponse);
}

message ScanResponse {
  string   scan_id    = 1;
  ScanState state     = 2;  // PENDING при принятии
  string   chunks_subject  = 3;  // куда слушать
  string   status_subject  = 4;
}
```

`StartScan` валидирует запрос и сразу возвращает (синхронная часть — быстрая). Скан запускается в фоновой goroutine.

**Идемпотентность:** если приходит StartScan с уже активным `scan_id` → `ALREADY_EXISTS`. Клиент решает: новый id или ждать существующий через `GetScanStatus`.

## 8. NATS JetStream

**Subjects (per-scan):**
- `harporis.chunks.<scan_id>` — `GitRowChunk` сообщения
- `harporis.status.<scan_id>` — события lifecycle (включая `output_config` для writer'а)
- `harporis.findings.<scan_id>` — от валидатора, геттер сюда не пишет (consumer создаётся writer'ом, за рамками этого дизайна)

**Streams:**
- `HARPORIS_CHUNKS` — wildcard `harporis.chunks.>`
- `HARPORIS_STATUS` — wildcard `harporis.status.>`
- `HARPORIS_FINDINGS` — wildcard `harporis.findings.>`

**Delivery:** at-least-once с ACK. `ack_wait=30s`. Валидатор должен быть идемпотентен, writer дедупит findings по `(scan_id, blob_sha, line_number, secret_type)`.

**Retention:** TTL 24h на сообщениях (после ack удаляются раньше).

**Сериализация:** protobuf на обоих каналах.

## 9. Lifecycle и состояния

```
PENDING (получен ScanRequest)
   ↓
RUNNING (walker запущен)
   ↓
(COMPLETED | PARTIAL | FAILED | CANCELLED)
```

**Status events:** при каждом переходе публикуются в `harporis.status.<scan_id>`:
```proto
message StatusEvent {
  string    scan_id   = 1;
  ScanState state     = 2;
  int64     timestamp = 3;
  string    message   = 4;          // human-readable
  ScanMetrics metrics = 5;          // blobs_scanned, chunks_published, ...
  OutputConfig output_config = 6;   // в event:scan_started, чтобы writer мог подхватить
}
```

**PARTIAL семантика:** если некоторые блобы упали на cat-file или scan был отменён ПОСЛЕ публикации хотя бы одного чанка. Лучше частичных результатов чем «всё или ничего».

**Cancellation:** `CancelScan(scan_id)` → close scan `context.Context` → walker и workers видят `ctx.Done()` → flush текущего чанка → emit EOS marker → exit. cat-file subprocesses kill'аются.

**Graceful shutdown сервиса (SIGTERM):** stop accepting new scans, in-flight доводим до EOS, cleanup work dirs, exit.

## 10. Обработка ошибок

| Failure                        | Стратегия                                                                       |
|--------------------------------|---------------------------------------------------------------------------------|
| git clone fails (auth/net)     | status FAILED, error в gRPC response, cleanup work dir                          |
| cat-file fails mid-stream      | log + metric, продолжаем со следующего блоба; >10% fail → status PARTIAL        |
| NATS publish timeout           | exp backoff 3 попытки (1s/2s/4s); persistent → status FAILED                    |
| Disk full                      | status FAILED, cleanup                                                          |
| Memory pressure                | GOMEMLIMIT смягчает; если OOM-kill → systemd restart                            |
| pipe broken (cat-file died)    | worker spawn'ит новый subprocess, продолжает с place                            |
| SIGTERM                        | graceful: drain in-flight, EOS markers, exit                                    |

## 11. Конфигурация

`services/getter/config/getter.yaml`:

```yaml
service:
  name: getter
  grpc_port: 50051
  log_level: info

workspace:
  work_dir: /var/lib/harporis/scans
  cleanup_on_complete: true

resources:
  max_cpu_cores: 4    # 0 = NumCPU
  max_ram_mb: 512     # 0 = no limit

git:
  clone_timeout_seconds: 600
  cat_file_batch_buffer_kb: 64

chunking:
  row_size_target_kb: 256
  row_overlap_lines: 64
  diff_context_lines: 30
  max_file_size_mb: 10

filters:
  path_exclusions:
    - ".git/"
    - "node_modules/"
    - "vendor/"
    - "dist/"
    - "build/"
    - "target/"
    - ".next/"
    - ".venv/"
    - "__pycache__/"
  binary_extensions:
    # см. полный список выше
    - ".png"
    - ".jpg"
    # ...

nats:
  url: ${NATS_URL:-nats://localhost:4222}
  jetstream:
    chunks_stream: HARPORIS_CHUNKS
    status_stream: HARPORIS_STATUS
    publish_ack_wait_seconds: 5

allow_request_overrides:
  - chunking.max_file_size_mb
  - chunking.row_size_target_kb
  - resources.max_ram_mb
```

**Правила (regex'ы для детекта) НЕ здесь.** Они в `services/scanner/config/rules/*.yaml` — только валидатор их грузит.

Валидация конфига на старте, fail-fast при ошибках.

## 12. Метрики и логирование

Per-scan структурированный лог (slog) + Prometheus метрики:

- `harporis_getter_blobs_scanned_total{scan_id}`
- `harporis_getter_blobs_skipped_total{scan_id, reason}` — reason ∈ {binary_extension, size_cap, path_excluded, gitattributes_binary, nul_byte}
- `harporis_getter_chunks_published_total{scan_id, kind}`
- `harporis_getter_bytes_published_total{scan_id}`
- `harporis_getter_errors_total{scan_id, type}` — type ∈ {cat_file, nats_publish, clone, ...}
- `harporis_getter_scan_duration_seconds{scan_id, status}`
- `harporis_getter_active_scans_gauge`

## 13. Структура кода

```
services/getter/
├── cmd/getter/main.go        # entrypoint: config load, GOMAXPROCS/GOMEMLIMIT, gRPC server
├── config/getter.yaml
├── internal/
│   ├── config/               # struct, parser, validation
│   ├── grpc/                 # gRPC server + handlers
│   ├── scan/                 # ScanContext, state machine
│   ├── git/
│   │   ├── walker.go         # rev-list, ls-tree, dedup
│   │   ├── catfile.go        # cat-file --batch wrapper
│   │   ├── diff.go           # unified diff parser
│   │   └── clone.go          # clone, auth, local-path detection
│   ├── chunk/
│   │   ├── builder.go        # accumulate lines, overlap, emit
│   │   └── scanner.go        # bufio.Scanner с custom max line size
│   ├── filter/
│   │   ├── filter.go         # 5-layer shouldScan
│   │   └── gitattributes.go
│   ├── nats/
│   │   ├── publisher.go      # JetStream publish с retry
│   │   └── streams.go        # ensure streams on startup
│   ├── resource/
│   │   ├── runtime.go        # GOMAXPROCS, GOMEMLIMIT setup
│   │   └── pools.go          # sync.Pool для буферов
│   └── metrics/              # Prometheus exposition
└── go.mod
```

## 14. Тестовая стратегия

- **Unit:**
  - Парсер unified diff (edge cases: пустой hunk, no newline at EOF, бинарные хунки, переименования)
  - Chunk builder: overlap correctness, граница ровно `row_size`, файл из одной строки, очень длинные строки
  - 5-layer filter, каждый слой отдельно
  - Config parser + validation (negative values, missing required, env substitution)
- **Integration:**
  - Локальный fake git-репо в `testdata/` (через `git init` в тестовом setup)
  - Full pipeline тест: каждый ScanType → ожидаемое множество чанков
  - In-process NATS (`natsserver` testing pkg)
  - Cancellation mid-scan, проверка cleanup
- **Property-based (gopter / rapid):**
  - Reconstruction: для случайного blob, разрезанного на чанки с overlap, multiline-секрет любой длины ≤ overlap должен быть найден ровно в одном чанке
- **Edge cases:**
  - Пустой файл, файл из одной строки, файл ровно RowSize байт
  - CRLF vs LF vs CR mix
  - Файл без trailing newline
  - UTF-8 multi-byte на границе чанка
  - Большой блоб (>10 МБ → skip), много мелких блобов
  - Один blob в 1000 коммитах (dedup)
  - Симлинки в git tree (skip)

## 15. Открытые вопросы и v2 кандидаты

- **Rich refs** для FULL_HISTORY: сейчас в `refs[]` одна first-seen запись. В v2 — собирать N последних, или предоставлять lazy lookup через writer→getter.
- **Внутри-скан параллельность walker'а**: если профайл покажет walker как bottleneck на гигантских репо (>100k коммитов), v2 может делить commit-диапазон между walker-горутинами.
- **Bare-клон оптимизация**: для всех ScanType кроме `HEAD_DIFF` working tree не нужен, экономим ~2× диска.
- **Shallow/partial clone**: для `CURRENT_STATE`/`STAGED`/`HEAD_DIFF` достаточно последних N коммитов, ускоряет первоначальный fetch для больших remote репо.
- **Repo cache** между сканами: при повторных сканах одного URL — `git fetch` вместо полного clone. Усложняет concurrency (file lock на cache dir).
- **Resume scan** при идемпотентном retry: сейчас reject `ALREADY_EXISTS`. Можно добавить checkpoint-based resume в v2.
