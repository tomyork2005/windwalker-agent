# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

Skywalker VPN **agent** — a Go service that runs on each VPN node and manages local network drivers (currently Xray) on behalf of a remote control-plane. The Go module is named `agent` (see `go.mod`), so all internal imports are `agent/internal/...` — do not rename.

## Commands

Build / run / test (PowerShell-friendly forms):

```
go build ./...
go build -o ./bin/skywalker-agent ./cmd/agent
go build -o ./bin/xray-status ./cmd/xray-status

go test ./...
go test ./internal/service -run TestService_UpsertUser -v

go vet ./...
```

Running the agent locally requires `CONFIG_PATH` pointing at a YAML config — `config.MustLoadConfig` calls `log.Fatal` if it is unset or the file is missing:

```
CONFIG_PATH=./config/config.yaml go run ./cmd/agent
```

Regenerate gRPC code from the proto (`api/control/control.proto`):

```
protoc --go_out=. --go-grpc_out=. api/control/control.proto
```

Deploy to a Linux host (installs Go if needed, Xray, writes configs, builds agent, installs systemd unit):

```
sudo CONTROL_PLANE_ADDR=<host:port> XRAY_DOMAIN=<domain> ./deploy/bootstrap-host.sh
```

## Architecture

The agent is a **single long-running client** that connects to the control-plane, receives tasks, and applies them to one or more network drivers. Layers, top-down:

1. **`cmd/agent/main.go`** — composition root. Wires storage → driver → multiplexer → service → transport, then blocks on `transportClient.Run`.
2. **`internal/transport`** — gRPC client for `api/control` (`ControlPlaneClient.Workstream`, a bidi stream). `Run` → `runOnce` loop with exponential backoff reconnect; `runOnce` sends `AgentHello`, then spawns `writer` (heartbeats + queued `AgentToControl` messages) and `reader` (receives `Welcome` / `Task`). Inbound `Task` messages are decoded by `RouteTask` in `handlers.go`, which calls into `TaskHandlers` and replies with `Ack`/`Nack` via the send queue. The `Welcome.agent_id` is stored back into `cfg` and also pushed into the global slog context via `logx.Set`.
3. **`internal/service`** — business logic. `Service` implements `transport.TaskHandlers`. Every mutating op runs inside `TxManager.WithTx`, checks `meta.Seq` against `GetLastAppliedSeq` (skip if already applied — this is the **idempotency contract** with the control-plane), applies the change to the driver, then the storage, then advances `last_applied_seq`. Keep this order when extending: driver first, storage second, seq last.
4. **`internal/driver`** — `Driver` interface (`Name/Upsert/Remove`) plus `Multiplexer` that routes by `user.DriverType`. `XrayDriver` talks to a local Xray instance two ways: via `systemctl` for lifecycle, and via Xray's own gRPC APIs (`proxyman/command` for user add/remove on an inbound tag, `stats/command` for counters). Xray identifies users by **email**, and this codebase deliberately uses `user.ID` (UUID) as that email.
5. **`internal/storage`** — sqlite-backed (`modernc.org/sqlite`, pure Go, no CGO). The schema lives in `initSQLiteSchema` and is versioned via `PRAGMA user_version`: to add a migration, extend the `switch ver` chain and bump the version at the end of your case. `TxManager.WithTx` stashes `*sql.Tx` on the context; `Storage.ex(ctx)` transparently uses either the tx or the bare DB, so a single `Storage` method works both inside and outside a transaction. Connection pool is pinned to `MaxOpenConns(1)` because sqlite is single-writer — do not raise this.
6. **`internal/logx`** — thin wrapper over `log/slog` with a global logger in an `atomic.Value`. Use `logx.Info/Error/Debug` for one-shots, `logx.With(...)` for per-operation child loggers, `logx.Set(...)` to add keys to the global base (transport does this after `Welcome`).
7. **`internal/config`** — cleanenv-based YAML loader. `MustLoadConfig` generates a fresh `InstanceID` UUID on every startup; `AgentID` comes back from the server via `Welcome`, not config.
8. **`api/control`** — generated protobuf/gRPC for `vpn.control.v1.ControlPlane`. The agent is the client; the control-plane lives in another repo.

### Message flow on the wire

```
Agent  --AgentHello-->                Control
Agent  <--Welcome{agent_id}--         Control
Agent  <--Task{upsert|remove|stats}-- Control   (repeated)
Agent  --Ack{seq} or Nack{seq,err}--> Control
Agent  --Heartbeat{agent_id,uptime}-> Control   (every heartbeat_period)
Agent  --StatsAll / StatsUser------>  Control   (in response to stats tasks)
```

Every server-initiated `Task` carries `TaskMeta.seq`; the agent must either ack or nack with the same seq. Seq is monotonic per agent and used for dedup on reconnect.

### Two binaries

- **`cmd/agent`** — the long-running daemon described above.
- **`cmd/xray-status`** — standalone diagnostic CLI that hits an HTTP `/stats` endpoint (not the Xray gRPC API — this is a separate listener, configured outside this repo). Reads `XRAY_LISTENER__API_BASE` / `XRAY_LISTENER_API_TOKEN` env vars or `--base` / `--token` flags. Unrelated to the agent's runtime path.

## Conventions worth knowing

- Xray user identity = `user.ID` as UUID, reused as the Xray `email` field. `XrayDriver.toXrayUser` enforces this.
- `driver_xray.protocol` must be `vless` or `vmess`; `XrayDriver.toXrayUser` returns an error for anything else.
- Tests use `testify`. Service and transport layers are covered with table-driven tests and hand-written mocks (see `internal/service/service_test.go`, `internal/transport/handlers_test.go`) — follow that pattern rather than introducing a mocking library.
- `go.mod` declares `go 1.25`; prefer modern stdlib (`log/slog`, `context`, `errors.Is/As`, etc.).