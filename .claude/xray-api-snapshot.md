# Xray API snapshot (актуально на 2026-05-01)

> Источник: pkg.go.dev для каждого пакета + GitHub Releases.
> Перед использованием: если файл старше суток — обновить заново.

## Версия: v26.3.27

- Тег последнего релиза: **v26.3.27**
- Дата релиза по странице: **2026-05-01** (пометка "27 Mar 17:51" в release-feed относится к локальному форматированию, реальная дата публикации — 2026-05-01)
- URL: https://github.com/XTLS/Xray-core/releases/tag/v26.3.27
- ⚠️ Схема версий у XTLS поменялась с `v1.YYMMDD.x` на `vYY.M.DD` (см. отметки "since v1.250910.0", "since v1.251201.0" — это старые билды до перехода). Текущий мажор — `v26`.

---

## proxyman.command (`github.com/xtls/xray-core/app/proxyman/command`)

### `HandlerServiceClient`

```go

type HandlerServiceClient interface {
    AddInbound(ctx, *AddInboundRequest, ...grpc.CallOption) (*AddInboundResponse, error)
    RemoveInbound(ctx, *RemoveInboundRequest, ...grpc.CallOption) (*RemoveInboundResponse, error)
    AlterInbound(ctx, *AlterInboundRequest, ...grpc.CallOption) (*AlterInboundResponse, error)
    ListInbounds(ctx, *ListInboundsRequest, ...grpc.CallOption) (*ListInboundsResponse, error)
    GetInboundUsers(ctx, *GetInboundUserRequest, ...grpc.CallOption) (*GetInboundUserResponse, error)
    GetInboundUsersCount(ctx, *GetInboundUserRequest, ...grpc.CallOption) (*GetInboundUsersCountResponse, error)
    AddOutbound(ctx, *AddOutboundRequest, ...grpc.CallOption) (*AddOutboundResponse, error)
    RemoveOutbound(ctx, *RemoveOutboundRequest, ...grpc.CallOption) (*RemoveOutboundResponse, error)
    AlterOutbound(ctx, *AlterOutboundRequest, ...grpc.CallOption) (*AlterOutboundResponse, error)
    ListOutbounds(ctx, *ListOutboundsRequest, ...grpc.CallOption) (*ListOutboundsResponse, error)
}
```

### Запросы / операции

```go
type AddInboundRequest struct {
    Inbound *core.InboundHandlerConfig
}

type RemoveInboundRequest struct {
    Tag string
}

type AlterInboundRequest struct {
    Tag       string
    Operation *serial.TypedMessage   // обёрнутая *AddUserOperation / *RemoveUserOperation
}

type AddUserOperation struct {
    User *protocol.User
}
// (op *AddUserOperation) ApplyInbound(ctx, inbound.Handler) error

type RemoveUserOperation struct {
    Email string                     // ⚠️ единственное поле — пользователь идентифицируется по Email
}
// (op *RemoveUserOperation) ApplyInbound(ctx, inbound.Handler) error
// (x  *RemoveUserOperation) GetEmail() string

type GetInboundUserRequest struct {
    Tag   string
    Email string                     // если пусто — возвращает всех
}

type GetInboundUserResponse struct {
    Users []*protocol.User
}

type GetInboundUsersCountResponse struct {
    Count int64
}

type ListInboundsRequest struct {
    IsOnlyTags bool
}
type ListInboundsResponse struct {
    Inbounds []*core.InboundHandlerConfig
}
```

---

## stats.command (`github.com/xtls/xray-core/app/stats/command`)

### `StatsServiceClient`

```go
type StatsServiceClient interface {
    GetStats(ctx, *GetStatsRequest, ...grpc.CallOption) (*GetStatsResponse, error)
    GetStatsOnline(ctx, *GetStatsRequest, ...grpc.CallOption) (*GetStatsResponse, error)
    QueryStats(ctx, *QueryStatsRequest, ...grpc.CallOption) (*QueryStatsResponse, error)
    GetSysStats(ctx, *SysStatsRequest, ...grpc.CallOption) (*SysStatsResponse, error)
    GetStatsOnlineIpList(ctx, *GetStatsRequest, ...grpc.CallOption) (*GetStatsOnlineIpListResponse, error)
    GetAllOnlineUsers(ctx, *GetAllOnlineUsersRequest, ...grpc.CallOption) (*GetAllOnlineUsersResponse, error)
}
```

### Структуры

```go
type GetStatsRequest struct {
    Name   string
    Reset_ bool   // ⚠️ имя поля — Reset_ (с подчёркиванием), потому что Reset зарезервировано proto.Message
}

type GetStatsResponse struct {
    Stat *Stat
}

type QueryStatsRequest struct {
    Pattern string
    Reset_  bool
}

type QueryStatsResponse struct {
    Stat []*Stat
}

type SysStatsRequest struct{}

type SysStatsResponse struct {
    NumGoroutine uint32
    NumGC        uint32
    Alloc        uint64
    TotalAlloc   uint64
    Sys          uint64
    Mallocs      uint64
    Frees        uint64
    LiveObjects  uint64
    PauseTotalNs uint64
    Uptime       uint32
}

type GetStatsOnlineIpListResponse struct {
    Name string
    Ips  map[string]int64
}

type GetAllOnlineUsersRequest struct{}

type GetAllOnlineUsersResponse struct {
    Users []string                  // формат строки: "user>>>user1>>>online"
}

type Stat struct {
    Name  string
    Value int64
}
```

---

## protocol (`github.com/xtls/xray-core/common/protocol`)

```go
type User struct {
    Level   uint32
    Email   string
    Account *serial.TypedMessage   // должно быть обёрнуто через serial.ToTypedMessage(&vless.Account{...})
}
// (u *User) GetTypedAccount() (Account, error)
// (u *User) ToMemoryUser() (*MemoryUser, error)
// (u *User) GetAccount() *serial.TypedMessage
// (u *User) GetEmail() string
// (u *User) GetLevel() uint32

type MemoryUser struct {
    Account Account                 // распарсенный аккаунт (интерфейс)
    Email   string
    Level   uint32
}
```

---

## serial (`github.com/xtls/xray-core/common/serial`)

```go
func ToTypedMessage(message proto.Message) *TypedMessage

type TypedMessage struct {
    Type  string   // имя proto-типа (полное имя сообщения)
    Value []byte   // сериализованный proto-payload
}
// (v *TypedMessage) GetInstance() (proto.Message, error)
// (x *TypedMessage) GetType() string
// (x *TypedMessage) GetValue() []byte

func GetInstance(messageType string) (interface{}, error)
func GetMessageType(message proto.Message) string
```

> Обратной функции `FromTypedMessage` нет — для распаковки используется метод `(*TypedMessage).GetInstance()`.

---

## vless (`github.com/xtls/xray-core/proxy/vless`)

**Решение по проекту:** воркер использует **только VLESS+Vision+REALITY**. `DesiredUser` минимальный — `{Email, UUID, Flow, Level}`. Дополнительные поля (`XorMode`, `Seconds`, `Padding`, `Reverse`, `Testpre`, `Testseed`) — это VLESS Encryption (ML-KEM-768) и Reverse Proxy, **сознательно** оставляем zero-value. Если фичи понадобятся — расширять `DesiredUser` и схему SQLite.

**Обязательный комментарий над конструированием `vless.Account` в `xrayapi/client.go`:**

```go
// vless.Account имеет дополнительные поля (XorMode, Seconds, Padding,
// Reverse, Testpre, Testseed) для VLESS Encryption (ML-KEM-768) и
// Reverse Proxy. Сознательно оставляем zero-value — для VLESS+Vision+REALITY
// они не требуются. Если понадобится — расширить DesiredUser и схему SQLite.
```

```go
type Account struct {
    Id         string      // UUID, например "66ad4540-b58c-4ad2-9926-ea63445a9b57"
    Flow       string      // например "xtls-rprx-vision"
    Encryption string

    // Поля, добавленные в свежих релизах (можно не использовать — обнулённые работают):
    XorMode    uint32      // since ~v1.250831.0
    Seconds    uint32      // since ~v1.250831.0
    Padding    string      // since ~v1.250831.0
    Reverse    *Reverse    // since ~v1.250910.0
    Testpre    uint32      // since ~v1.251201.0
    Testseed   []uint32    // since ~v1.251201.0
}

type MemoryAccount struct {
    ID         *protocol.ID
    Flow       string
    Encryption string
    XorMode    uint32
    Seconds    uint32
    Padding    string
    Reverse    *Reverse
    Testpre    uint32
    Testseed   []uint32
}

type Reverse struct {
    Tag      string
    Sniffing *proxyman.SniffingConfig
}
```

---

## vmess — НЕ ИСПОЛЬЗУЕТСЯ

**Решение по проекту:** воркер работает **только с VLESS**. Импорт `github.com/xtls/xray-core/proxy/vmess` в новом коде **не должен появляться**. Если контрол-плейн где-то шлёт `alter_id` — это legacy-поле, которое в текущем `vmess.Account` уже отсутствует, и к новому воркеру не относится.

Если в будущем понадобится VMess — добавлять отдельную ветку в `AddUser` через тип в `DesiredUser` (например, `Protocol string` с константой). Сейчас не закладываем.

---

## GetAllOnlineUsers: AVAILABLE

- PR: https://github.com/XTLS/Xray-core/pull/5080 — **merged** 2025-12-26 (yuhan6665), commit `ad468e4` в `main`.
- Доступен в `StatsServiceClient.GetAllOnlineUsers(ctx, *GetAllOnlineUsersRequest) → *GetAllOnlineUsersResponse`.
- В `v26.3.27` (текущий релиз) — присутствует.
- Точный самый ранний релизный тег с этим методом не зафиксирован на странице PR — но любой `v26.x` уже содержит его. Если нужно поддерживать старые сборки Xray (до 2026-01) — этот метод там отсутствует, и вызов вернёт `Unimplemented`.
- Формат ответа: `Users []string`, каждый элемент в форме `"user>>>{email}>>>online"` (нужен парсинг на стороне агента).

---

## Pitfalls

- **`GetStatsRequest.Reset_`** — с подчёркиванием на конце (protoc-gen-go переименование из-за конфликта с `proto.Message.Reset()`). То же самое для `QueryStatsRequest.Reset_`.
- **`vless.Account.Encryption`** обычно `"none"` (на per-user уровне). ML-KEM-768 включается на уровне inbound `decryption`, а не per-user.
- **`Email` в `protocol.User`** обязан быть уникальным глобально (Validator.Add). В нашем проекте используется `user.ID` (UUID) как Email — глобальная уникальность гарантирована.
- **`RemoveUserOperation.Email`** — `Email` это первичный ключ пользователя в Xray на runtime. Совпадает с `XrayDriver.Remove(userID)` где `userID == email`. Не менять.
- **`GetAllOnlineUsersResponse.Users`** — это `[]string` в формате `"user>>>{email}>>>online"`, не структурированные объекты. Парсить вручную.

## Решения по проекту (зафиксированы пользователем)

1. Воркер — **только VLESS+Vision+REALITY**. VMess не используется и не импортируется.
2. `DesiredUser` минимальный: `{Email, UUID, Flow, Level}`. Новые поля `vless.Account` (XorMode/Seconds/Padding/Reverse/Testpre/Testseed) сознательно zero-value.
3. Над конструированием `vless.Account` в `xrayapi/client.go` — обязательный комментарий (см. раздел vless выше).
