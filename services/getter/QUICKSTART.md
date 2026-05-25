# Getter: быстрый старт

Инструкция для того, кто впервые запускает getter и хочет проверить, что всё работает. Два пути: через **docker compose** (быстрее, ничего не ставить) и **нативно** (полезно для разработки и тестов).

## Что такое getter за 30 секунд

Getter — это один из сервисов Harporis. Он берёт git-репозиторий (локальный или удалённый), режет его файлы построчно на куски по 256 КБ и публикует эти куски в NATS. Что с ними делать дальше — забота валидатора (это другой сервис).

Getter сам по себе **не ищет** секреты. Он только готовит данные.

Управлять getter'ом удобно через `getter-cli` — маленький бинарь рядом с самим getter'ом.

---

## Путь A: через docker compose (рекомендую)

### Что нужно

- **Docker** + **docker compose v2** (`docker compose version` должен напечатать `v2.x`)
- **git** на хосте (для CLI и для подготовки тестового репо)
- **Go 1.26+** на хосте — нужен только для сборки `getter-cli` (бинарь крошечный, ничего не ставит в систему)

### 1. Запустить стек

Из корня репозитория:

```bash
docker compose up --build -d
```

Поднимутся два контейнера: `harporis-nats` (JetStream-брокер) и `harporis-getter`. Проверь:

```bash
docker compose ps
# должны быть оба со статусом Up; nats — (healthy)
```

В логах getter'а должна быть строчка `getter ready`:

```bash
docker compose logs getter | tail -3
```

### 2. Собрать CLI на хосте

```bash
cd services/getter && make build-cli
# появится bin/getter-cli
```

### 3. Подложить тестовое репо

Из коробки `docker-compose.yml` монтирует `/tmp/demo-repo` хоста в getter как `/repos/demo` (read-only). Создай это репо:

```bash
mkdir -p /tmp/demo-repo && cd /tmp/demo-repo
git init -b main -q
git config user.email t@t.t && git config user.name t
echo 'const SECRET = "AKIAIOSFODNN7EXAMPLE"' > config.js
echo 'package main' > main.go
git add -A && git commit -q -m seed
```

### 4. Запустить скан

```bash
./services/getter/bin/getter-cli scan \
  --type current_state \
  --local /repos/demo \
  --scan-id demo-1 \
  --wait
```

Что должно вывестись:

```
submitted scan_id=demo-1 type=CURRENT_STATE
[2026-…Z] RUNNING   | scan started   | scanned=0 skipped=0 chunks=0 bytes=0 errors=0
[2026-…Z] COMPLETED | scan finished  | scanned=2 skipped=0 chunks=2 bytes=49 errors=0
```

Ключевое:
- `--local` — это путь **внутри контейнера**, не на хосте.
- `--wait` подписывается на `harporis.status.<scan_id>` и держит CLI до терминального состояния.

### 5. Отменить скан, если передумал

```bash
./services/getter/bin/getter-cli cancel demo-1 --reason "ой, не тот репо"
```

В логе getter'а scan перейдёт в `CANCELLED`, в финальном статус-эвенте поле `Message` будет `"scan cancelled: ой, не тот репо"`. Если scan уже COMPLETED — cancel просто ничего не сделает.

Cancel работает в любой момент жизни скана: между этапами walker'а, во время фетча блобов, в момент публикации чанков.

### 6. Посмотреть метрики

`/metrics` getter'а проброшен на хост:

```bash
curl -s localhost:9100/metrics | grep harporis_getter | head -10
```

Найдёшь:

```
harporis_getter_active_scans 0
harporis_getter_blobs_scanned_total{scan_id="demo-1"} 2
harporis_getter_chunks_published_total{kind="BLOB",scan_id="demo-1"} 2
harporis_getter_bytes_published_total{scan_id="demo-1"} 49
harporis_getter_scan_duration_seconds_count{scan_id="demo-1",status="COMPLETED"} 1
```

### 7. Снести всё

```bash
docker compose down -v
```

`-v` снесёт и volumes (NATS-стримы, getter work_dir).

### Сканировать другое репо с хоста

Раскомментируй / добавь в `docker-compose.yml` ещё одну строку под getter'ом:

```yaml
    volumes:
      - getter-work:/var/lib/harporis/scans
      - /tmp/demo-repo:/repos/demo:ro
      - /home/me/projects/big-repo:/repos/big:ro   # ← вот здесь
```

`docker compose up -d --force-recreate getter` и запускай `getter-cli scan --local /repos/big …`.

### Сканировать удалённое репо

`/repos/demo` тогда не нужен. CLI клонирует репо сам, getter в контейнере хранит клон в volume `getter-work`:

```bash
# публичный репо
./bin/getter-cli scan --type current_state --remote-url https://github.com/foo/bar.git --wait

# с PAT
./bin/getter-cli scan --remote-url https://github.com/foo/private.git --remote-token ghp_xxx --wait

# по SSH-ключу
./bin/getter-cli scan --remote-url git@github.com:foo/bar.git --remote-ssh-key ~/.ssh/id_ed25519 --wait
```

---

## Путь B: нативно, без Docker

### Что нужно

1. **Go 1.26+** — `go version`
2. **git 2.40+** — `git --version`
3. **nats-server** с JetStream — поставить локально из [релизов](https://github.com/nats-io/nats-server/releases)

### Запуск

```bash
# 1) NATS в одном терминале
nats-server -js

# 2) сборка
cd services/getter && make build      # → bin/getter и bin/getter-cli
make test                              # все тесты должны быть OK

# 3) getter в другом терминале
./bin/getter --config config/getter.yaml --metrics-port 9100

# 4) скан с того же хоста
./bin/getter-cli scan --local /tmp/demo-repo --wait
```

NATS_URL берётся из env: по умолчанию `nats://localhost:4222`. Переопределить — `NATS_URL=nats://host:port ./bin/getter-cli ...`.

---

## Шпаргалка по CLI

```
getter-cli scan    [flags]                    отправить ScanRequest
getter-cli cancel  <scan-id> [--reason "..."] отменить активный скан
getter-cli watch   <scan-id>                  подписаться на статус-стрим

# полная справка
getter-cli help
```

Типы сканов:

| Флаг `--type` | Что делает |
|---|---|
| `current_state` (default) | Сканит файлы на HEAD |
| `full_history` | Все коммиты во всех ветках, дедуп по `blob_sha` |
| `branch_full` (`--branch X`) | Все коммиты, достижимые с X |
| `commit_range` (`--from A --to B`) | Коммиты из диапазона `A..B` |
| `branch_diff` (`--branch X --base-branch Y`) | `git diff Y..X` |
| `head_diff` | Незакоммиченные изменения (worktree) |
| `staged` | `git diff --cached` |

Виды авторизации к remote-репо:

| Сценарий | Флаги |
|---|---|
| Публичный HTTPS | `--remote-url https://...` |
| HTTPS + PAT (GitHub-style) | `--remote-url https://... --remote-token ghp_xxx` |
| SSH с приватным ключом | `--remote-url git@... --remote-ssh-key /path/to/key` |
| SSH с пиннингом host key | + `--remote-known-hosts /path/to/known_hosts` |
| SSH через ssh-agent | `--remote-url git@...` (без `--remote-ssh-key`) |

Гарантии безопасности: токены и пароли **не попадают** в `/proc/<pid>/cmdline`, не пишутся в логи (фильтр в getter), приватный ключ хранится во временном файле `0600` и удаляется после клона.

---

## Что делать если что-то не работает

| Симптом | Где смотреть |
|---|---|
| `docker compose up` падает на пуле образов | `docker pull nats:2.10-alpine` руками; реестр может моргать |
| Скан мгновенно в FAILED, в логе getter `dubious ownership` | Mount UID конфликтует с UID `harporis` в контейнере. В Dockerfile уже `git config --global --add safe.directory '*'`; пересобери (`docker compose build getter --no-cache`) |
| `missing go.sum entry` при `make build` | `go mod tidy` в `services/getter/` и `contracts/` |
| CLI говорит `usage: getter-cli cancel <scan-id>` | Передал scan-id, но он попал во флаг. Запусти `getter-cli cancel demo-1 --reason …` или с флагами впереди — CLI понимает оба порядка |
| `getter-cli scan` зависает на `--wait` | Подписка пытается достать status-стрим, getter не публикует. Проверь `docker compose logs getter`: вероятно, ошибка обработчика. Метрика `harporis_getter_errors_total` покажет тип |
| getter молчит, метрика `active_scans` всегда 0 | ScanRequest не дошёл. `nats stream view HARPORIS_REQUESTS` (нужен внешний CLI `nats`) — если сообщение там есть и не уходит → ack-wait слишком короткий или MaxAckPending исчерпан |

## Куда смотреть дальше

- Полный референс — `services/getter/README.md`
- Дизайн и решения — `docs/superpowers/specs/2026-05-23-getter-design.md`
- Известные ограничения — секция «Known limitations» в README
