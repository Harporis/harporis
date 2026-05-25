# Getter: быстрый старт

Эта инструкция — для того, кто впервые запускает getter у себя на машине и хочет проверить, что всё работает. Простыми словами, без воды.

## Что такое getter за 30 секунд

Getter — это один из сервисов Harporis. Он берёт git-репозиторий (локальный или удалённый), режет его файлы построчно на куски по 256 КБ и публикует эти куски в NATS. Что с ними делать дальше — забота сканера/валидатора (это другой сервис).

Getter сам по себе НЕ ищет секреты. Он только готовит данные.

## Что должно стоять на машине

1. **Go 1.26+** — `go version` должен напечатать `go1.26.x`
2. **git 2.40+** — `git --version`
3. **nats-server** с JetStream — два варианта:
   - Поставить локально: `https://github.com/nats-io/nats-server/releases` (один бинарь)
   - Запустить в Docker: `docker run --rm -p 4222:4222 nats:latest -js`

Сам getter ничего больше не требует — никакой БД, никакого Kafka.

## Шаг 1. Собрать бинарь

```bash
cd services/getter
make build
```

После этого должен появиться `bin/getter`. Если упало с ошибкой `missing go.sum entry` — выполни `go mod tidy` и повтори.

## Шаг 2. Прогнать тесты

```bash
make test
```

Тесты сами поднимают встроенный NATS — внешний запускать НЕ надо. Должно быть `ok` по всем пакетам и финальное «PASS». Если что-то красное, см. секцию [Что делать если тест упал](#что-делать-если-тест-упал).

Один тест намеренно длинный (`TestRequestSubscriber_LongHandlerKeptAliveByHeartbeat` ≈ 4-5 секунд) — проверяет, что долгий скан не убивается ack-wait таймаутом.

## Шаг 3. Запустить getter живьём

### 3.1 Поднять NATS

В отдельном терминале:

```bash
nats-server -js
```

или

```bash
docker run --rm -p 4222:4222 nats:latest -js
```

Должно появиться `Server is ready`.

### 3.2 Подправить конфиг (если нужно)

Конфиг лежит в `services/getter/config/getter.yaml`. По умолчанию все настройки нормальные для локального запуска. Что может понадобиться поменять:

| Поле | Зачем |
|---|---|
| `workspace.work_dir` | Куда складывать клоны удалённых репо. Дефолт `/var/lib/harporis/scans` — на dev-машине лучше поменять на что-то вроде `/tmp/harporis-getter` |
| `resources.max_cpu_cores` | Сколько параллельных воркеров запускать. Дефолт 4 |
| `nats.url` | Если NATS не на `localhost:4222` |

Любое поле можно переопределить переменной окружения через `${VAR}` или `${VAR:-default}` прямо в YAML.

### 3.3 Запустить getter

```bash
./bin/getter --config config/getter.yaml --metrics-port 9100
```

Должно появиться примерно:

```
{"time":"...","level":"INFO","msg":"getter ready","nats":"nats://localhost:4222","grpc":"[::]:50051","metrics":9100}
```

Если NATS не запущен или адрес другой, увидишь `FATAL: nats dial:`.

## Шаг 4. Запустить пробный скан

Самый быстрый способ убедиться, что getter работает — попросить его просканить какой-нибудь локальный репо.

### 4.1 Подготовь тестовый репо

```bash
mkdir /tmp/test-repo && cd /tmp/test-repo
git init -b main
echo 'const SECRET = "AKIAIOSFODNN7EXAMPLE"' > config.js
echo 'package main' > main.go
git add -A
git commit -m "seed"
```

### 4.2 Отправь ScanRequest

ScanRequest — это protobuf-сообщение. Готового CLI пока нет ([пункт 1 в Known limitations README](README.md#known-limitations)), но можно отправить через любой proto-encoder. Самый простой способ — крошечный Go-скрипт:

```bash
cat > /tmp/scan.go <<'EOF'
package main

import (
	"fmt"
	"os"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/contracts/wire"
)

func main() {
	nc, err := nats.Connect("nats://localhost:4222")
	if err != nil { fmt.Println(err); os.Exit(1) }
	defer nc.Close()
	js, _ := nc.JetStream()

	req := &v1.ScanRequest{
		ScanId: "test-scan-1",
		Type:   v1.ScanType_CURRENT_STATE,
		Source: &v1.Source{Src: &v1.Source_LocalPath{LocalPath: "/tmp/test-repo"}},
	}
	data, _ := proto.Marshal(req)
	if _, err := js.Publish(wire.ScansRequestsSubject, data); err != nil {
		fmt.Println(err); os.Exit(1)
	}
	fmt.Println("submitted scan", req.ScanId)
}
EOF

cd services/getter
go run /tmp/scan.go
```

Должно напечатать `submitted scan test-scan-1`.

### 4.3 Посмотри, что получилось

В логе getter'а должны появиться статус-события:

```
{"level":"INFO","msg":"...","scan_id":"test-scan-1","state":"RUNNING"}
{"level":"INFO","msg":"...","scan_id":"test-scan-1","state":"COMPLETED"}
```

А на `http://localhost:9100/metrics` появятся метрики:

```bash
curl -s localhost:9100/metrics | grep harporis_getter | head
```

Ищи:
- `harporis_getter_blobs_scanned_total{scan_id="test-scan-1"} 2` — два файла (config.js + main.go) отсканированы
- `harporis_getter_chunks_published_total{scan_id="test-scan-1",kind="BLOB"} 2` — два чанка опубликованы
- `harporis_getter_active_scans 0` — скан завершился

Если эти цифры не нули — getter работает. Содержимое чанков лежит в стриме `HARPORIS_CHUNKS` на NATS — посмотреть, например, через `nats stream view HARPORIS_CHUNKS` (CLI `nats` ставится отдельно).

## Виды авторизации к remote репо

Если хочешь сканить НЕ локальный репо, а удалённый, в `ScanRequest.Source.remote` указываешь URL и нужный набор полей. Гетеру можно дать креды любым из этих способов:

| Тип репо | Что заполнить |
|---|---|
| Публичный HTTPS | `Source.remote.url = "https://github.com/foo/bar.git"` — больше ничего не нужно |
| HTTPS + GitHub PAT (token) | `url` + `Source.remote.token = "ghp_..."` |
| HTTPS + Basic (логин/пароль) | `url` + `Source.remote.basic.username = "alice"` + `password = "..."` (как именно класть Basic — см. ниже, в контракте это пока один Token; для Basic используется connection-string `https://user:pass@host/...` либо отдельный auth объект — этот режим открывается следующим шагом контракта) |
| SSH с приватным ключом | `url = "git@github.com:foo/bar.git"` + `Source.remote.ssh.private_key_pem = "..."` + опционально `known_hosts` |
| SSH через ssh-agent | `url = "git@..."` без `ssh` блока. Getter подхватит `SSH_AUTH_SOCK` от своего окружения |

Какие гарантии безопасности по token/паролю:
- **Токены и пароли НЕ попадают в `/proc/<pid>/cmdline`** (раньше они там были — это исправлено).
- В лог при ошибке клона тоже не попадают — getter их редактирует.
- Приватный SSH-ключ пишется во временный файл с правами `0600` и удаляется после клона.

## Что делать если тест упал

Чаще всего одно из:

- **`missing go.sum entry`** — выполни `go mod tidy` в `services/getter/` и `contracts/`.
- **`fatal: not a git repository`** — у тебя нет `git` в PATH или используется крайне старая версия. Поставь свежий git.
- **NATS-тесты висят** — встроенный NATS не смог открыть свой порт. Это редкость, обычно достаточно перезапустить тест: `go test ./internal/nats/... -count=1`.
- **Линукс-only поведение в cat-file тестах** — все тесты должны работать на любом POSIX, но если ты на Windows — рассчитывай на WSL, нативно сейчас getter не тестируется.

## Что делать если getter не публикует чанки

1. **Проверь, что NATS жив.** `nats-server --help` или `nc -z localhost 4222` (должен сразу вернуться).
2. **Проверь, что стримы созданы.** Поставь CLI `nats` и сделай `nats stream ls`. Должны быть `HARPORIS_REQUESTS`, `HARPORIS_CHUNKS`, `HARPORIS_STATUS`, `HARPORIS_FINDINGS`.
3. **Проверь, что ScanRequest долетел.** `nats stream view HARPORIS_REQUESTS` — если там твоё сообщение, значит дошло до брокера. Если потом исчезло — значит getter его съел.
4. **Посмотри логи getter'а.** В JSON-логе ищи `scan request handler` (Error) — это значит обработчик упал.
5. **Метрика `harporis_getter_errors_total`** — если она > 0, в `type` лейбле будет место поломки (`process_blob`, `publish_chunk`, `publish_diff`).

## Сколько ресурсов реально нужно

| Ситуация | CPU | RAM | Диск |
|---|---|---|---|
| Маленький локальный репо (~ 100 файлов) | 1 ядро | 50 МБ | 0 (не клонирует) |
| Большой репо (50k файлов, 5 ГБ) | 4 ядра | ~500 МБ | равен размеру clone |
| Полная история (`FULL_HISTORY` на 10k коммитов) | 4 ядра | ~500 МБ | clone-size + бэкап JS-стримов |

Жёсткий бюджет по RAM настраивается полем `resources.max_ram_mb` — Go сам начнёт активнее собирать мусор.

## Куда смотреть дальше

- Полный референс — `services/getter/README.md`
- Дизайн и решения — `docs/superpowers/specs/2026-05-23-getter-design.md`
- Список того, что НЕ доделано — секция «Known limitations» в README
