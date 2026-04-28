# CLAUDE.md

Этот файл — инструкция для Claude Code (claude.ai/code) при работе с кодом репозитория.

## Проект

Skywalker VPN **agent** — Go-сервис, запускаемый на каждой VPN-ноде. Управляет локальными сетевыми драйверами (сейчас только Xray) по командам удалённого контрол-плейна. Имя Go-модуля — `agent` (см. `go.mod`), поэтому все внутренние импорты идут через `agent/internal/...` — переименовывать нельзя.

Контрол-плейн живёт в соседнем репозитории: `C:\Users\Handi\GolandProjects\windwalker-controlplane`. Прото-файл `api/control/control.proto` — общий, синхронизирован в обоих репах вручную; при правке прото нужно регенерировать код в обоих местах.

## Команды

Сборка / запуск / тесты (формы, дружественные к PowerShell):

```
go build ./...
go build -o ./bin/skywalker-agent ./cmd/agent
go build -o ./bin/xray-status ./cmd/xray-status

go test ./...
go test ./internal/service -run TestService_UpsertUser -v
go test ./internal/service -run TestService_RemoveUser -v

go vet ./...
```

Локальный запуск агента требует переменной `CONFIG_PATH`, указывающей на YAML-конфиг — `config.MustLoadConfig` зовёт `log.Fatal`, если переменная не задана или файла нет:

```
CONFIG_PATH=./config/config.yaml go run ./cmd/agent
```

Регенерация gRPC-кода из прото (`api/control/control.proto`):

```
protoc --go_out=. --go-grpc_out=. api/control/control.proto
```

Деплой на Linux-хост (ставит Go при необходимости, Xray, конфиги, собирает агента, ставит systemd-юнит):

```
sudo CONTROL_PLANE_ADDR=<host:port> XRAY_DOMAIN=<domain> ./deploy/bootstrap-host.sh
```

## Архитектура

Агент — это **один долгоживущий клиент**, который коннектится к контрол-плейну, принимает таски и применяет их к одному или нескольким сетевым драйверам. Слои сверху вниз:

1. **`cmd/agent/main.go`** — composition root. Связывает storage → driver → multiplexer → service → transport и блокируется на `transportClient.Run`.
2. **`internal/transport`** — gRPC-клиент для `api/control` (`ControlPlaneClient.Workstream`, bidi-стрим). `Run` → цикл `runOnce` с экспоненциальным backoff'ом для реконнекта; `runOnce` шлёт `AgentHello`, затем поднимает `writer` (heartbeat'ы + очередь сообщений `AgentToControl`) и `reader` (приём `Welcome` / `Task`). Входящие `Task` декодируются в `RouteTask` (`handlers.go`), который вызывает методы `TaskHandlers` и отвечает `Ack`/`Nack` через очередь отправки. `Welcome.agent_id` записывается обратно в `cfg` и пробрасывается в глобальный slog-контекст через `logx.Set`.
3. **`internal/service`** — бизнес-логика. `Service` реализует `transport.TaskHandlers`. Любая мутирующая операция выполняется внутри `TxManager.WithTx`, проверяет `meta.Seq` против `GetLastAppliedSeq` (если уже применена — пропускаем; это **контракт идемпотентности** с контрол-плейном), применяет изменение к драйверу, потом к storage, потом сдвигает `last_applied_seq`. Этот порядок (driver → storage → seq) обязателен при расширении.
4. **`internal/driver`** — интерфейс `Driver` (`Name/Upsert/Remove/BuildCreds`) и `Multiplexer`, который маршрутизирует по `user.DriverType`. `XrayDriver` общается с локальным Xray двумя путями: через `systemctl` — для жизненного цикла, и через собственные gRPC API Xray (`proxyman/command` для add/remove юзеров на inbound-теге, `stats/command` для счётчиков). Xray идентифицирует юзеров по **email**, и в этом коде сознательно используется `user.ID` (UUID) в качестве email.
5. **`internal/storage`** — sqlite на `modernc.org/sqlite` (чистый Go, без CGO). Схема живёт в `initSQLiteSchema`, версионируется через `PRAGMA user_version`: чтобы добавить миграцию, продли цепочку `switch ver` и бампни версию в конце своего кейса. `TxManager.WithTx` кладёт `*sql.Tx` в контекст; `Storage.ex(ctx)` прозрачно подбирает либо tx, либо голую DB — один и тот же метод `Storage` работает и внутри транзакции, и снаружи. Пул соединений жёстко зафиксирован на `MaxOpenConns(1)`, потому что у sqlite один писатель — поднимать нельзя.
6. **`internal/logx`** — тонкая обёртка над `log/slog` с глобальным логгером в `atomic.Value`. Используй `logx.Info/Error/Debug` для разовых записей, `logx.With(...)` — для per-операционных child-логгеров, `logx.Set(...)` — для добавления ключей в глобальную базу (транспорт это делает после `Welcome`).
7. **`internal/config`** — YAML-загрузчик на cleanenv. `MustLoadConfig` генерирует свежий UUID `InstanceID` на каждый старт; `AgentID` приходит с сервера через `Welcome`, в конфиге его нет.
8. **`api/control`** — сгенерированный protobuf/gRPC для `vpn.control.v1.ControlPlane`. Агент — клиент, контрол-плейн — отдельный репозиторий.

### Поток сообщений по сети

```
Agent  --AgentHello-->                Control
Agent  <--Welcome{agent_id}--         Control
Agent  <--Task{upsert|remove|stats}-- Control   (повторяется)
Agent  --Ack{seq} или Nack{seq,err}--> Control
Agent  --Heartbeat{agent_id,uptime}-> Control   (каждые heartbeat_period)
Agent  --StatsAll / StatsUser------>  Control   (в ответ на stats-таски)
```

Каждый серверный `Task` несёт `TaskMeta.seq`; агент обязан либо ack'нуть, либо nack'нуть с тем же seq. Seq монотонен per-agent и используется для dedup при реконнекте.

### Два бинарника

- **`cmd/agent`** — основной демон, описанный выше.
- **`cmd/xray-status`** — отдельная диагностическая CLI, ходит на HTTP-эндпойнт `/stats` (не Xray gRPC API — это отдельный listener, конфигурируется вне этого репо). Читает `XRAY_LISTENER__API_BASE` / `XRAY_LISTENER_API_TOKEN` или флаги `--base` / `--token`. К рантайму агента отношения не имеет.

## Интеграция с контрол-плейном

Контрол-плейн (`windwalker-controlplane`) — Go-сервис на Postgres + bidi gRPC. По биллингу/подпискам он ведёт `subscriptions`; на стороне агента ведёт `agents`, `agent_tasks` (очередь команд), `agent_seq` (per-agent счётчик), `outbox_events` (transactional outbox).

**Поток `Remove` (отмена подписки → удаление из Xray):**

1. Подписка отменяется (TG-бот / истечение / админ) → запись `subscription_cancelled` в `outbox_events`.
2. `internal/workers/worker.go` каждые 3 секунды поллит outbox и для `EventTypeSubscriptionCancelled` зовёт `AgentSender.StopUserSubscribe` (`internal/service/agent-sender.go:77`).
3. `AgentSender` находит `agent_id` через активную подписку юзера и зовёт `Dispatcher.DispatchRemove` (`internal/service/dispatcher.go:75`).
4. `Dispatcher` под транзакцией: `NextSeq(agent_id)` (атомарный INSERT/UPDATE на `agent_seq`) + `EnqueueTask` со сложенным `AgentRemovePayload{UserID, DriverType}` в `agent_tasks` (kind=`remove`). Параллельно отдаёт операцию в in-memory `Hub` сессии — если агент онлайн, шлётся сразу.
5. На реконнекте `RecoverPending` подтягивает все таски с `done_at IS NULL` и `retries < 10`, шлёт повторно. Авторетраев через таймер нет — только при новом коннекте.
6. Прото `UserRemoveRequest{user_id, driver_type}` собирается в `internal/transport/agent/converter.go:53`. На уровне агента: `RouteTask` (`handlers.go:51`) → `Service.RemoveUser` (`service/service.go:106`) → `Multiplexer.Remove` (`driver/multiplexer.go:38`) → `XrayDriver.Remove` (`driver/xray.go:137`), который вызывает `proxyman.HandlerService.AlterInbound` с `RemoveUserOperation{Email: userID}`. Затем `Storage.RemoveUser` (`DELETE FROM users WHERE id=?`) и `SetLastAppliedSeq`.
7. Агент шлёт `Response_Remove{}` (пустой) с тем же `meta.seq`. На стороне контрол-плейна `MarkTaskAck` ставит `done_at`, `ok=true` в `agent_tasks`.

**Идемпотентность:**
- `meta.seq` — транспортная: на агенте сравнивается с `last_applied_seq`, при `seq <= last` таска тихо игнорируется.
- `meta.request_id` — бизнес: на стороне контрол-плейна это `subscription_id`. Агент эту ось пока не использует.

**Известные пробелы по `Remove` (на 2026-04-28):**
- `XrayDriver.Remove` не покрыт юнит-тестами (в `xray_test.go` есть только тесты `BuildCreds`).
- Сценарий «driver OK, storage DELETE падает» откатит транзакцию, но в Xray юзер уже удалён → состояния разъедутся. Компенсации нет.
- Ошибки от Xray не различаются по типу: «юзера нет» (что для `Remove` фактически идемпотентно) идёт как ошибка и превращается в Nack.
- `XrayDriver.ListUsers` намеренно возвращает ошибку — сверки реального состояния Xray с storage нет.
- На стороне контрол-плейна обработчик `Response_Remove` в `internal/transport/agent/server.go` помечен как «not implemented yet» — это требует проверки/фикса в репо контрол-плейна, иначе таски `remove` могут не закрываться в `agent_tasks` после успешного ack'а.

## Полезные конвенции

- Identity юзера в Xray = `user.ID` (UUID), переиспользуется как Xray `email`. Это форсится в `XrayDriver.toXrayUser`.
- `driver_xray.protocol` обязан быть `vless` или `vmess`; `XrayDriver.toXrayUser` для остального вернёт ошибку.
- Тесты — на `testify`. Слои service и transport покрыты табличными тестами с ручными моками (см. `internal/service/service_test.go`, `internal/transport/handlers_test.go`) — следуй этому стилю, не тащи отдельные мок-библиотеки.
- `go.mod` объявляет `go 1.25`; предпочитай современную стандартную библиотеку (`log/slog`, `context`, `errors.Is/As` и т.п.).