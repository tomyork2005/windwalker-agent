# CLAUDE.md

Этот файл — инструкция для Claude Code (claude.ai/code) при работе с кодом репозитория.

## Проект

Skywalker VPN **agent** — Go-сервис, запускаемый на каждой VPN-ноде. Управляет локальным Xray по командам удалённого контрол-плейна. Имя Go-модуля — `agent` (см. `go.mod`), все внутренние импорты идут через `agent/internal/...` — переименовывать нельзя.

Контрол-плейн живёт в соседнем репозитории: `C:\Users\Handi\GolandProjects\windwalker-controlplane`. Прото-файл `api/control/control.proto` — общий, синхронизирован в обоих репах вручную; при правке прото нужно регенерировать код в обоих местах.

## Команды

Сборка / запуск / тесты — всё дерево зелёное:

```
go build ./...
go test ./internal/drivers/xray ./internal/storage ./internal/service ./internal/transport -v
go vet ./...
```

Локальный запуск агента требует переменной `CONFIG_PATH`, указывающей на YAML-конфиг — `config.MustLoadConfig` пишет `slog.Error` и зовёт `os.Exit(1)`, если переменная не задана, файла нет или обязательное поле пустое (`agent_id`, `transport.address`, `driver_xray.public_host/sni/public_key/short_id`, `storage.path`):

```
CONFIG_PATH=./config/config.yaml go run ./cmd
```

Накатить миграции (нужно перед первым запуском и после правок схемы — код агента их **не катит**):

```
goose -dir migrations sqlite3 ./data/agent.db up
```

Регенерация gRPC-кода из прото (`api/control/control.proto`). Опции `paths=source_relative` обязательны — иначе protoc уйдёт по `option go_package = "control/api/control;..."` и сложит файлы в посторонний `control/` каталог:

```
protoc --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative api/control/control.proto
```

Регенерация моков (`internal/service/mocks/`, `internal/transport/mocks/`):

```
go generate ./internal/service ./internal/transport
```

One-command деплой на чистую Linux-VPS (ставит Go ≥ 1.25 при необходимости, Xray, goose; генерит REALITY-ключи, патчит `config/config.yaml`, собирает агента, накатывает миграции, ставит и запускает systemd-юниты):

```
sudo XRAY_DOMAIN=<domain> CONTROL_PLANE_ADDR=<host:port> AGENT_ID=<node-id> ./deploy.sh
```

## Архитектура

Агент — это **один долгоживущий клиент**, который коннектится к контрол-плейну, принимает таски и применяет их к локальному Xray. Слои:

1. **`cmd/main.go`** — composition root. Грузит конфиг, ставит slog JSON-handler как default, поднимает `signal.NotifyContext(SIGINT|SIGTERM)`, конструирует storage → driver → service → transport, гоняет `service.Run` и `transport.Run` под `errgroup.WithContext`. На отмену контекста — корректное завершение через defer'ы `Close()`.
2. **`internal/transport`** — gRPC-клиент для `api/control` (`ControlPlaneClient.Workstream`, bidi-стрим): `AgentHello` → `Welcome` → поток `Task` (upsert/remove) с ответами `Response` и периодическими `Stats` (top-level). Реконнект с экспоненциальным backoff'ом. Реализует `service.Sender`, чтобы worker мог писать ack'и/ошибки/stats прямо в исходящий стрим. См. раздел «Транспорт» ниже.
3. **`internal/service`** — бизнес-логика поверх воркера. `Service` принимает входящие команды от транспорта (`UpsertUser`, `DeleteTask`) и кладёт их в БД через `TaskRepo.SaveTask`; всю работу по применению тасок к Xray и периодическим лупам делает `Worker` (см. раздел «Воркер» ниже). Идемпотентность — по `request_id` из `Task` (это PK таблицы `tasks`).
4. **`internal/drivers/xray`** — текущая, актуальная реализация драйвера Xray (см. ниже). Лаконичный публичный интерфейс через методы `*Driver`, без обёртки-мультиплексера: агент работает только с одним драйвером.
5. **`internal/storage`** — sqlite на `modernc.org/sqlite` (чистый Go, без CGO). Реализует `service.TaskRepo` целиком: `SaveTask`/`PullPendingTasks`/`MarkDone`/`MarkFailed`/`ListUserIDs`/`UpsertUser`/`DeleteUser`. Схема живёт в `migrations/` (см. раздел «Схема БД» ниже), формат — goose (`-- +goose Up` / `-- +goose Down`); миграции **катаются снаружи** через goose CLI — код агента их не применяет. Пул соединений жёстко зафиксирован на `MaxOpenConns(1)`, потому что у sqlite один писатель — поднимать нельзя. Чтения мапятся через `github.com/georgysavva/scany/v2/sqlscan` (аналог `pgxscan` для `database/sql`); записи — обычный `db.ExecContext`. Транзакций наружу не выставляется (никакого `TxManager`).
6. **`internal/model`** — общие доменные типы (`User`, `UserUsage`, `Task`, `TaskKind`, `OutboundUpsert`/`OutboundRemove`/`OutboundError`/`OutboundStats`); используется драйвером, сервисом и транспортом, чтобы не тащить proto-типы наружу.
7. **`internal/config`** — YAML-загрузчик на cleanenv, всё логирование через `log/slog` (никаких `log.Fatal`). Структура `Config` плоская: верхнеуровневые `Env`/`AgentID` плюс четыре под-конфига — `Storage` (`SQLiteConfig`), `DriverXray` (`XrayConfig`), `Transport` (`TransportGrpcConfig`), `Worker` (`WorkerConfig`). `MustLoadConfig` валидирует обязательные поля (`agent_id`, `transport.address`, REALITY-параметры в `driver_xray`, `storage.path`), генерирует свежий `InstanceID` (UUID) на каждый старт и копирует `cfg.AgentID` в `cfg.Transport.AgentID` и `cfg.Worker.AgentID`. Поля `WorkerConfig` совпадают по порядку/именам/типам со `service.WorkerConfig` — в `cmd/main.go` идёт через прямую конверсию `service.WorkerConfig(cfg.Worker)`. `Welcome.agent_id` транспорт сейчас только логирует и НЕ пробрасывает обратно в worker — при расхождении `cfg.AgentID` и серверного будет mismatch в `OutboundStats.AgentID` (известное ограничение, ждёт agent_id discovery).
8. **`api/control`** — сгенерированный protobuf/gRPC для `vpn.control.v1.ControlPlane`. Агент — клиент, контрол-плейн — отдельный репозиторий.

### Поток сообщений по сети (новый контракт)

```
Agent  --AgentHello-->                  Control
Agent  <--Welcome{agent_id}--           Control
Agent  <--Task{upsert|remove}--         Control   (повторяется)
Agent  --Response{request_id, ...}-->   Control
Agent  --Stats{users[]}-->              Control   (top-level, периодически)
```

Идемпотентность транспорта — `request_id` per-task; повтор той же таски при реконнекте безопасен.

## Драйвер Xray (`internal/drivers/xray`)

Публичный API — методы `*xray.Driver`:

```go
AddUser(ctx, userID string) error
RemoveUser(ctx, userID string) error
ListUsers(ctx) ([]string, error)              // вернёт UUIDы
CollectStats(ctx) ([]*model.UserUsage, error) // абсолютные cumulative-счётчики
BuildCreds(userID string) (string, error)     // готовая vless://... строка
Close() error
```

Драйвер общается **только с уже работающим** Xray по его gRPC API (`proxyman/command` для add/remove/list юзеров на inbound-теге, `stats/command` для счётчиков). `systemctl`/жизненный цикл процесса — забота оператора и `deploy.sh`, не драйвера.

### Email convention

Xray идентифицирует юзеров по строке email. Драйвер хранит соответствие `userID ↔ email` через жёсткий шаблон `<userID>@xray.com` — функции `emailFor` / `userIDFromEmail` в `xray.go`. `ListUsers` тихо пропускает юзеров с email вне этого шаблона (логирует `slog.Warn`), чтобы не считать «чужих» (вставленных мимо агента) своими.

### Идемпотентность

- **`AddUser`** — сначала `GetInboundUsers(tag, email)`; если юзер уже есть — возвращаем `nil`. Дополнительная страховка от race: если `AlterInbound` вернул ошибку с подстрокой `"already"` (case-insensitive) → `nil`.
- **`RemoveUser`** — если `AlterInbound` вернул ошибку с подстрокой `"not found"` → `nil`. Это решает классический race «юзер уже удалён, но контрол-плейн перепосылает».
- **`CollectStats`** — `Reset_=false`, отдаёт абсолютные cumulative-счётчики Xray (с момента старта процесса); вычисление дельт за окно — обязанность вызывающего слоя. `IPCount` — снимок «сейчас» через `GetStatsOnlineIpList` per user; `codes.NotFound` → `0`.
- **`BuildCreds`** — чистая функция от `cfg + userID`; результат — VLESS+REALITY URI без fragment'а.

Подстрочный matching ошибок Xray (`errMatches` в `xray.go`) — необходимое зло: gRPC API Xray не возвращает структурированных кодов для «уже есть» / «не найдено».

### Конфиг (`config.XrayConfig`)

Поля:
- `APIAddr`, `InboundTag`, `OpTimeout` — параметры подключения и тайм-аут per-RPC.
- `Flow`, `Level` — параметры юзера на стороне Xray (`vless.Account.Flow`, `protocol.User.Level`); единые для всех юзеров этого инбаунда.
- `PublicHost`, `Port`, `SNI`, `PublicKey`, `ShortID`, `Fingerprint` — REALITY client-facing параметры; используются в `BuildCreds` для генерации URI.

Всё, что относилось к `systemctl` и протоколам помимо vless, удалено из конфига сознательно. Драйвер работает **только с VLESS+Vision+REALITY**.

### Структура пакета

- `xray.go` — `Driver` struct, `New`, `Close`, email helpers, `errMatches`, константа `emailDomain`.
- `users.go` — `AddUser`, `RemoveUser`, `ListUsers`.
- `stats.go` — `CollectStats`, `parseStatName`, `fetchOnlineIPCount`.
- `creds.go` — `BuildCreds`.
- `*_test.go` — табличные юнит-тесты на pure-функции (`emailFor`/`userIDFromEmail`/`parseStatName`/`BuildCreds`). gRPC-методы (`AddUser`/`RemoveUser`/`ListUsers`/`CollectStats`) юнит-тестами **не покрыты** — будут покрыты интеграционными тестами против живого Xray в отдельной итерации.

## Транспорт (`internal/transport`)

Один долгоживущий `Client`, конструируется через `transport.New(cfg, log)`. Имплементирует `service.Sender`, поэтому передаётся в `NewAgentService` как зависимость. `Run(ctx, svc Service)` блокируется до отмены ctx; внутри — reconnect-цикл с экспоненциальным backoff'ом (`ReconnectMin` ↑×2 до `ReconnectMax`).

Каждая «сессия» — это: `connect` (ленивый dial, один `*grpc.ClientConn` живёт между сессиями) → `Workstream` → `sendHello` → `recvWelcome` → `errgroup` с двумя горутинами:

- **`writer`** дренит общий буферизованный канал `sendQ` (`SendQueueSize` или дефолт 128) и шлёт в `stream.Send`. На ctx-cancel — `CloseSend()` и выход. Известное окно потерь: сообщение, выдернутое из канала и упавшее на `Send`, не восстанавливается; worker ретраит upsert/remove через таблицу `tasks`, а stats периодические — допустимо.
- **`reader`** читает входящие `ControlToAgent`. После `recvWelcome` (он жуётся синхронно до старта горутин) reader ждёт только `Task`'ов; всё остальное (включая повторный Welcome) попадает в `default` switch'а и тихо логируется как `unknown control message`.

`Sender`-методы (`SendUpsertAck`/`SendRemoveAck`/`SendError`/`SendStats`) — все по одному паттерну: `model → proto → enqueue`. `enqueue` — `select` на `c.sendQ <- m` vs `<-ctx.Done()`; backpressure by design (полная очередь блокирует worker, ctx разблокирует). Никакого non-blocking `default`-кейса не нужно.

Маршрутизация входящих тасок — `handleTask`: `Task_Upsert` → `svc.UpsertUser`, `Task_Remove` → `svc.DeleteTask`. Если service вернул ошибку (это значит `repo.SaveTask` упал, таска НЕ легла нам в очередь) — синтезируем `Response{request_id, error}` через `SendError` и кладём в очередь; reader не выходит.

### Структура пакета

- `client.go` — `Client` struct, интерфейс `Service` (то, что транспорт зовёт наружу — `UpsertUser`/`DeleteTask`), `New`, `Run`, `runSession`, `connect`/`closeConn`, `sendHello`/`recvWelcome`, `reader`/`writer`, `handleControlMessage`/`handleTask`, `nextBackoff`.
- `sender.go` — 4 метода `Sender` + `enqueue`.
- `convert.go` — чистые конвертеры `proto ↔ model` (`userFromProto`, `upsertAckToProto`, `removeAckToProto`, `errorToProto`, `statsToProto`).
- `genmocks.go` (build tag `generate`) — minimock-директива на `Service`. Моки в `internal/transport/mocks/`.
- `convert_test.go` — табличные тесты конвертеров через `proto.Equal`.
- `sender_test.go` — drain-the-channel: вызвал `SendX`, прочитал из `sendQ`, сравнил `proto.Equal` (без моков). Плюс кейсы на ctx-cancel и full-queue-blocking.
- `router_test.go` — табличные тесты `handleControlMessage` с `ServiceMock` — стиль ровно как в `internal/service/worker_test.go` (helper'ы для моков и клиента, `t.Parallel`, `Expect().Return()`).
- `client_test.go` — bufconn end-to-end happy path: in-process gRPC-сервер, hello/welcome handshake, Task → Response roundtrip через инжект `ServiceMock`. Запуск с `-race` требует CGO.

## Воркер (`internal/service`)

`Worker.Run(ctx)` через `errgroup` поднимает три параллельных тикера; падение любого валит всю группу.

1. **Task loop** (`TaskPollInterval`, default 1s) — `repo.PullPendingTasks(limit)` → для каждой таски `processTask`:
   - `TaskUpsert`: `driver.AddUser(userID)` → `driver.BuildCreds(userID)` → `repo.UpsertUser(userID, driverType)` → `sender.SendUpsertAck`.
   - `TaskRemove`: `driver.RemoveUser(userID)` → `repo.DeleteUser(userID, driverType)` → `sender.SendRemoveAck`.
   - На ошибке любого шага: `sender.SendError` + `repo.MarkFailed(requestID, err)`. Failed-таска **терминальна**, ретраев нет — `PullPendingTasks` фильтрует `done_at IS NULL AND failed_at IS NULL`.
   - На успехе: `repo.MarkDone(requestID)`.

   Порядок шагов «driver → repo → ack» специально: драйвер — источник правды о реальном состоянии Xray, repo — наша durable-копия, ack — самый внешний шаг.
2. **Stats loop** (`StatsInterval`, default 30s) — `driver.CollectStats` → `sender.SendStats` (top-level Stats в исходящий стрим, с `AgentID` и `UptimeSeconds` от старта воркера).
3. **Sync loop** (`SyncInterval`, default 1m) — сверяет `repo.ListUserIDs` с `driver.ListUsers` через `diffUserSets` и фиксит дрифт: чего нет в Xray — `AddUser`, чего нет в БД — `RemoveUser`. Это страховка от расхождения между sqlite и состоянием Xray (рестарт ноды, ручная правка).

Зависимости воркера — три интерфейса в `worker.go`:

- `XrayDriver` — реализуется `internal/drivers/xray.Driver`.
- `TaskRepo` — реализуется `internal/storage.Storage` (`SaveTask`/`PullPendingTasks`/`MarkDone`/`MarkFailed`/`ListUserIDs`/`UpsertUser`/`DeleteUser`).
- `Sender` — реализуется `*transport.Client` (`SendUpsertAck`/`SendRemoveAck`/`SendError`/`SendStats`); технически кладёт сообщение в `sendQ`, оттуда writer-горутина транспорта пишет в исходящий gRPC-стрим.

Доменные DTO для `Sender` — в `internal/model/outbound.go` (`OutboundUpsert`/`OutboundRemove`/`OutboundError`/`OutboundStats`); `Task` и `TaskKind` — в `internal/model/types.go`.

Моки для всех трёх интерфейсов лежат в `internal/service/mocks/` и генерируются `minimock` через `go generate ./internal/service` (декларации — в `genmocks.go` под build tag `generate`). Тесты воркера используют именно эти моки. Транспорт пользуется тем же подходом — `internal/transport/mocks/ServiceMock`, см. секцию «Транспорт».

## Storage (`internal/storage`)

Реализация `service.TaskRepo` поверх `database/sql` + `modernc.org/sqlite`.

- `sqlite.go` — `Storage` struct, `New(ctx, cfg, log)`, `Close()`. В конструкторе — открытие БД, `MaxOpenConns(1)`, набор PRAGMA (WAL/synchronous/busy_timeout/foreign_keys + temp_store/cache_size/mmap_size). Миграции **не применяются** — это делает goose CLI снаружи.
- `storage.go` — методы интерфейса. Чтения (`PullPendingTasks`, `ListUserIDs`) — через `sqlscan.Select`; записи — `db.ExecContext`. Внутренний `taskRow` мапит INTEGER `received_at` ↔ `time.Time`.
- `storage_test.go` — интеграционный тест против временного sqlite-файла; миграция применяется хелпером `applyInitMigration` (читает `migrations/00001_init.sql`, выдёргивает Up-секцию, выполняет).

Семантика:
- `SaveTask` идемпотентен: `INSERT ... ON CONFLICT(request_id) DO NOTHING`. Повтор той же таски при реконнекте безопасен.
- `MarkDone`/`MarkFailed` для несуществующего/уже терминального `request_id` — `slog.Warn` + `nil`. Failed — терминально (не ретраится).
- `ListUserIDs` возвращает DISTINCT `user_id` (один юзер может быть под несколькими `driver_type`).

## Схема БД (`migrations/`)

Миграции — отдельные пронумерованные SQL-файлы под `migrations/`, формат goose (`-- +goose Up` / `-- +goose Down`). Применяются `goose` CLI снаружи (`goose -dir migrations sqlite3 ./data/agent.db up`); код агента миграции не катит. Чтобы добавить миграцию — клади следующий пронумерованный `NNNNN_*.sql`.

Текущий стейт (`00001_init.sql`) — две таблицы:

- **`tasks`** — журнал входящих тасок от контрол-плейна. PK = `request_id` (даёт сквозную идемпотентность: повторный `SaveTask` с тем же id — `ON CONFLICT DO NOTHING`). Колонки: `user_id`, `driver_type`, `kind ∈ ('upsert','remove')`, `received_at`, и терминальные `done_at` / `failed_at` + `last_error`. Партиал-индекс `idx_tasks_pending` (`WHERE done_at IS NULL AND failed_at IS NULL`) — для горячего пути `PullPendingTasks`; терминальные таски (как done, так и failed) в индекс не попадают.
- **`users`** — текущий состав юзеров на этом агенте, источник правды для sync-loop'а. Композитный PK `(user_id, driver_type)` — один и тот же `user_id` может жить под несколькими драйверами, если когда-нибудь появится не-Xray драйвер.

Все timestamp'ы в БД — unix seconds (`INTEGER`), не ISO-строки.

## Полезные конвенции

- Identity юзера в Xray = `userID` (UUID), email формируется как `userID + "@xray.com"`. Никогда не передавай в Xray API «голый» userID как email — иди через `emailFor`.
- Драйвер работает **только с VLESS+Vision+REALITY**. Импорт `github.com/xtls/xray-core/proxy/vmess` в новом коде не должен появляться.
- В свежем коде используй `log/slog` напрямую (`slog.Default()`, `*slog.Logger`); не заводи новые обёртки. `cmd/main.go` ставит JSON-handler в `slog.SetDefault`, уровень — `Info` для `env: prod`, `Debug` для `dev`/`local`.
- Тесты — на `testify` (`assert` / `require`). Стиль — табличные тесты с table-driven кейсами; ручные моки писать только когда нет другого выхода (никаких отдельных мок-библиотек).
- `go.mod` объявляет `go 1.25`; предпочитай современную стандартную библиотеку (`log/slog`, `context`, `errors.Is/As` и т.п.).

## Снапшот Xray API

Актуальные сигнатуры `proxyman.command` / `stats.command` / `vless.Account` / `protocol.User` / `serial.TypedMessage` лежат в `.claude/xray-api-snapshot.md`. Перед изменениями драйвера — сверяйся с ним, особенно с разделом «Pitfalls» (поле `Reset_` с подчёркиванием, формат `GetAllOnlineUsersResponse.Users`, и т.п.).
