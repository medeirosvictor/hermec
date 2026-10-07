# Hermec Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A working Hermec server and headless client: Ed25519 identity, versioned wire protocol, role-gated access, channels with presence and group chat — the base every later plan (voice, GUI, screenshare) builds on.

**Architecture:** Clients hold Ed25519 keypairs and authenticate to a self-hosted server via challenge-response over WebSocket. A versioned JSON protocol carries auth, channel membership, presence, and chat. Roles from server config gate every action. The core ships as importable Go packages; the server binary is their first consumer, the headless client their second (and the test harness).

**Tech Stack:** Go 1.23+, `github.com/gorilla/websocket`, `github.com/BurntSushi/toml`, stdlib `crypto/ed25519`.

**Spec:** `docs/specs/2026-10-06-hermec-design.md` (§3.3, §4, §5, §8 govern this plan; media §§ belong to later plans)

## Global Constraints

- Module path: `github.com/medeirosvictor/hermec`; Go `1.23` floor in `go.mod`.
- Dependencies for this plan: `gorilla/websocket` and `BurntSushi/toml` only; everything else stdlib.
- All code `gofmt`-clean; `go vet ./...` clean; full suite via `go test ./...` passes on Windows and Linux.
- Protocol version constant is `1`; every wire message is one JSON envelope `{"v":1,"type":"<string>","data":{...}}`.
- Max WebSocket message size: `64 * 1024` bytes, enforced server- and client-side.
- Key files written with permission `0600`; `Generate` must never overwrite an existing key file.
- Reserved-for-E2EE message type constants (`e2ee_key_exchange`, `e2ee_frame`) are declared and documented but have no handler (spec §8).
- Commit after every task; messages end with `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`.

## Review Focus

1. Malformed or oversized wire data (bad JSON, wrong version, >64KB frame) must produce a clean error/close, never a panic — fuzz + size tests in Task 2, connection test in Task 4.
2. Invalid auth (wrong signature, wrong password, key not on allowlist, reused nonce on a second connection) must reject and close, leaking nothing — tests in Task 4.
3. Chat sent to a channel the sender never joined, or by an identity lacking `send_chat`, must return a `TypeError` message and broadcast nothing — tests in Task 5.
4. A client that drops mid-channel (TCP cut, no Leave) must be removed from presence within one ping interval and must not panic the broadcaster — test in Task 6.
5. The same identity connecting twice (two devices, one key — spec §4 allows it) must yield two live sessions sharing a fingerprint, both receiving broadcasts — test in Task 6.

---

### Task 1: Identity (`core/identity`)

**Files:**
- Create: `go.mod`, `core/identity/identity.go`
- Test: `core/identity/identity_test.go`

**Interfaces:**
- Consumes: nothing (first task; run `go mod init github.com/medeirosvictor/hermec` here).
- Produces:
  - `type Identity struct` (unexported fields)
  - `func Generate() (*Identity, error)`
  - `func Load(path string) (*Identity, error)`
  - `func (id *Identity) Save(path string) error` — errors if file exists
  - `func (id *Identity) PublicKey() ed25519.PublicKey`
  - `func (id *Identity) Sign(msg []byte) []byte`
  - `func Verify(pub ed25519.PublicKey, msg, sig []byte) bool`
  - `func Fingerprint(pub ed25519.PublicKey) string` — lowercase base32 (no padding) of `sha256(pub)[:10]`, hyphenated in groups of 4, e.g. `k7mv-q3xp-9dfw-02hj`

- [ ] **Step 1: Write failing tests**

```go
func TestGenerateSignVerify(t *testing.T) // Generate; Sign([]byte("hello")); Verify true; Verify false on tampered msg
func TestSaveLoadRoundTrip(t *testing.T)  // Save to t.TempDir(); Load; same PublicKey; on Unix, stat perms == 0600
func TestSaveRefusesOverwrite(t *testing.T) // Save twice to same path; second call errors
func TestFingerprintStableAndFormatted(t *testing.T) // same key → same string; matches ^[a-z2-7]{4}(-[a-z2-7]{4}){3}$; differs for a second key
```

- [ ] **Step 2: Run `go test ./core/identity/ -v` — expect FAIL (package missing)**
- [ ] **Step 3: Implement `core/identity/identity.go`** — key file format: PEM-like single file holding the ed25519 seed, base64; any clear format is fine as long as Save/Load round-trips and the overwrite guard holds.
- [ ] **Step 4: Run `go test ./core/identity/ -v` — expect PASS**
- [ ] **Step 5: Commit** — `feat: ed25519 identity with key file and fingerprints`

### Task 2: Wire protocol (`core/proto`)

**Files:**
- Create: `core/proto/proto.go`
- Test: `core/proto/proto_test.go`, `core/proto/fuzz_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `const Version = 1`, `const MaxMessageSize = 64 * 1024`
  - `type Envelope struct { V int; Type string; Data json.RawMessage }` (json tags `v`, `type`, `data`)
  - `func Encode(msgType string, payload any) ([]byte, error)`
  - `func Decode(raw []byte) (Envelope, error)` — errors on invalid JSON, `V != Version`, empty `Type`, or `len(raw) > MaxMessageSize`
  - Type constants: `TypeChallenge="challenge"`, `TypeAuth="auth"`, `TypeAuthOK="auth_ok"`, `TypeError="error"`, `TypeJoin="join"`, `TypeLeave="leave"`, `TypePresence="presence"`, `TypeChatSend="chat_send"`, `TypeChatMessage="chat_message"`, `TypeChannelList="channel_list"`; reserved, no handlers: `TypeE2EEKeyExchange="e2ee_key_exchange"`, `TypeE2EEFrame="e2ee_frame"`
  - Payload structs (exact fields):
    - `Challenge{ Nonce []byte }`
    - `Auth{ PubKey []byte; Name string; Sig []byte; Password string }` — Sig is over the nonce
    - `AuthOK{ Fingerprint string; Roles []string; Channels []string }`
    - `ErrorMsg{ Code string; Message string }` — codes used in this plan: `"auth_failed"`, `"forbidden"`, `"bad_request"`, `"not_joined"`
    - `Join{ Channel string }`, `Leave{ Channel string }`
    - `Member{ Fingerprint string; Name string; Roles []string }`
    - `Presence{ Channel string; Members []Member }`
    - `ChatSend{ Channel string; Text string }`
    - `ChatMessage{ Channel string; From Member; Text string; TS time.Time }`

- [ ] **Step 1: Write failing tests**

```go
func TestEncodeDecodeRoundTrip(t *testing.T) // Encode(TypeChatSend, ChatSend{...}); Decode; envelope V==1, Type matches; json.Unmarshal(env.Data) equals input
func TestDecodeRejects(t *testing.T)         // table: not-json, `{"v":2,"type":"x"}`, `{"v":1,"type":""}`, 64KB+1 payload → all error, never panic
```

- [ ] **Step 2: Run `go test ./core/proto/ -v` — expect FAIL**
- [ ] **Step 3: Implement `core/proto/proto.go`**
- [ ] **Step 4: Add fuzz target**

```go
func FuzzDecode(f *testing.F) // seed with valid Encode output + the reject-table inputs; property: Decode never panics
```

- [ ] **Step 5: Run `go test ./core/proto/ -v` then `go test ./core/proto/ -fuzz=FuzzDecode -fuzztime=30s` — expect PASS, no crashers**
- [ ] **Step 6: Commit** — `feat: versioned wire protocol with fuzzed decoder`

### Task 3: Roles (`core/roles`)

**Files:**
- Create: `core/roles/roles.go`
- Test: `core/roles/roles_test.go`

**Interfaces:**
- Consumes: `identity.Fingerprint` strings as grant keys (no import needed; they are plain strings).
- Produces:
  - `type Permission string`; constants `PermJoinChannel Permission = "join_channel"`, `PermSendChat = "send_chat"`, `PermManage = "manage"` (manage is declared now, used by later plans)
  - `type Config struct { Roles map[string][]Permission; Grants map[string][]string; DefaultRoles []string }` — Grants maps fingerprint → role names; DefaultRoles apply to any authenticated identity absent from Grants
  - `func Builtin() map[string][]Permission` — `"admin"`: all three perms; `"user"` and `"bot"`: join_channel + send_chat
  - `func (c Config) RolesFor(fingerprint string) []string`
  - `func (c Config) Has(fingerprint string, p Permission) bool` — unknown role names in grants are ignored, not fatal

- [ ] **Step 1: Write failing tests**

```go
func TestHasViaGrant(t *testing.T)      // grant fp→["admin"] with Builtin(); Has(fp, PermManage) true
func TestDefaultRoles(t *testing.T)     // ungranted fp, DefaultRoles ["user"]; Has join/chat true, manage false
func TestNoDefaultMeansNothing(t *testing.T) // empty DefaultRoles; ungranted fp has no permissions
func TestUnknownRoleIgnored(t *testing.T)    // grant fp→["ghost"]; Has anything false, no panic
```

- [ ] **Step 2: Run `go test ./core/roles/ -v` — expect FAIL**
- [ ] **Step 3: Implement `core/roles/roles.go`**
- [ ] **Step 4: Run `go test ./core/roles/ -v` — expect PASS**
- [ ] **Step 5: Commit** — `feat: role and permission config`

### Task 4: Server with challenge-response auth (`server`)

**Files:**
- Create: `server/server.go`, `server/conn.go`
- Test: `server/auth_test.go`

**Interfaces:**
- Consumes: `core/proto` envelopes and payloads; `identity.Verify`, `identity.Fingerprint`; `roles.Config`.
- Produces:
  - `type Config struct { Addr string; Password string; AllowedKeys []string; Roles roles.Config; Channels []string }` — `AllowedKeys` are fingerprints; empty slice = open server; `Channels` are the server's static channel names for this plan
  - `func New(cfg Config) *Server`
  - `func (s *Server) Start() error` — listens (plain ws for MVP tests; TLS wiring lands in Task 7), serves in background
  - `func (s *Server) Addr() string` — actual host:port after Start (supports `:0` in tests)
  - `func (s *Server) Shutdown(ctx context.Context) error`
  - Auth flow (exact): on ws connect server sends `challenge` with a 32-byte random nonce → client replies `auth` → server checks: sig verifies over nonce, password matches (when set), fingerprint on allowlist (when non-empty). Any valid identity authenticates; roles only gate later actions. Success → `auth_ok` with fingerprint, roles, channel list; failure → `error{code:"auth_failed"}` then close. One auth attempt per connection.

- [ ] **Step 1: Write failing tests** (dial with `gorilla/websocket` directly; a helper `authAs(t, url, id, password)` keeps them short)

```go
func TestAuthHappyPath(t *testing.T)     // valid sig → auth_ok carries correct fingerprint, roles, channels
func TestAuthBadSignature(t *testing.T)  // sign wrong bytes → error code "auth_failed", conn closed
func TestAuthWrongPassword(t *testing.T) // server Password "s3cret", client sends "nope" → auth_failed
func TestAuthAllowlist(t *testing.T)     // AllowedKeys=[fpA]; identity B → auth_failed; identity A → auth_ok
func TestOversizedFrameCloses(t *testing.T) // send 70KB frame pre-auth → conn closed, server alive (next dial works)
```

- [ ] **Step 2: Run `go test ./server/ -v` — expect FAIL**
- [ ] **Step 3: Implement `server/server.go` (lifecycle, registry) and `server/conn.go` (per-connection read loop, auth state machine)** — set `SetReadLimit(proto.MaxMessageSize)`; one goroutine per conn reading, one writing via a buffered outbound channel.
- [ ] **Step 4: Run `go test ./server/ -v` — expect PASS; run `go vet ./...`**
- [ ] **Step 5: Commit** — `feat: hermec server with challenge-response auth`

### Task 5: Channels, presence, chat (`server`)

**Files:**
- Modify: `server/server.go`, `server/conn.go`
- Create: `server/channels.go`
- Test: `server/channels_test.go`

**Interfaces:**
- Consumes: Task 4's server and auth helper; `proto` Join/Leave/Presence/ChatSend/ChatMessage; `roles.Config.Has`.
- Produces (wire behavior later tasks and plans rely on):
  - `join` requires `PermJoinChannel`; unknown channel → `error{code:"bad_request"}`; success → full `presence` broadcast to every member of that channel (including the joiner)
  - `leave` and disconnect both trigger a `presence` broadcast to remaining members
  - `chat_send` requires membership of that channel AND `PermSendChat`; violations → `error{code:"not_joined"}` / `error{code:"forbidden"}`, nothing broadcast
  - valid chat → `chat_message` to all channel members including sender; `From` is the sender's `Member`; `TS` set by the server
  - server pings every 15s; a conn missing 2 pongs is closed and cleaned up

- [ ] **Step 1: Write failing tests**

```go
func TestJoinBroadcastsPresence(t *testing.T) // A joins, B joins → A receives presence with both members
func TestChatRoundTrip(t *testing.T)          // A,B joined; A chat_send → both receive chat_message, From.Fingerprint==fpA, server TS nonzero
func TestChatWithoutJoinRejected(t *testing.T)// B authed, not joined; chat_send → error "not_joined"; A receives nothing for 200ms
func TestJoinWithoutPermRejected(t *testing.T)// server with empty DefaultRoles; ungranted identity sends join → error "forbidden", no presence broadcast
func TestLeaveUpdatesPresence(t *testing.T)   // A,B joined; B leaves → A receives presence with only A
```

- [ ] **Step 2: Run `go test ./server/ -v` — expect FAIL on new tests**
- [ ] **Step 3: Implement `server/channels.go`** — channel registry guarded by one mutex; broadcasts iterate a member snapshot; sends to a full/dead outbound buffer drop the conn rather than block.
- [ ] **Step 4: Run `go test ./server/ -v` — expect PASS; run with `-race`**
- [ ] **Step 5: Commit** — `feat: channels, presence, and role-gated chat`

### Task 6: Headless client (`client`) + integration test

**Files:**
- Create: `client/client.go`
- Test: `client/client_test.go`, `integration/chat_test.go`

**Interfaces:**
- Consumes: `proto`, `identity`; a running `server.Server` in tests.
- Produces (Plans 2–4 build bots, the GUI, and test harnesses on exactly this):
  - `func Dial(ctx context.Context, url string, id *identity.Identity, name, password string) (*Client, error)` — performs the full auth flow; error if auth fails
  - `func (c *Client) Fingerprint() string`, `func (c *Client) Roles() []string`, `func (c *Client) Channels() []string` (from auth_ok)
  - `func (c *Client) Join(ctx context.Context, channel string) error` — resolves on the next presence for that channel or an error message
  - `func (c *Client) SendChat(ctx context.Context, channel, text string) error`
  - `func (c *Client) Events() <-chan Event` with `type Event struct { Chat *proto.ChatMessage; Presence *proto.Presence; Err error }` — exactly one field non-nil; channel closes when the connection dies
  - `func (c *Client) Close() error`

- [ ] **Step 1: Write failing unit tests** (`client/client_test.go` spins a real `server.New`)

```go
func TestDialAuthAndJoin(t *testing.T) // Dial; Fingerprint matches identity; Join("general") returns nil
func TestDialBadPassword(t *testing.T) // Dial returns error containing "auth_failed"
```

- [ ] **Step 2: Run `go test ./client/ -v` — expect FAIL**
- [ ] **Step 3: Implement `client/client.go`** — one read goroutine demuxing into Events and pending Join/auth waits.
- [ ] **Step 4: Run `go test ./client/ -v` — expect PASS**
- [ ] **Step 5: Write failing integration tests** (`integration/chat_test.go`)

```go
func TestTwoClientsChat(t *testing.T)      // full stack: server + Dial A, B; both Join; A sends; assert B's Events yields the ChatMessage with A's fingerprint
func TestDroppedClientLeavesPresence(t *testing.T) // force-close B's underlying conn (Close without Leave); A receives presence without B before test timeout
func TestSameKeyTwoSessions(t *testing.T)  // Dial twice with ONE identity (two "devices"); both Join; a chat reaches both sessions; presence lists the fingerprint (either dedup'd or twice — assert the chosen behavior and document it in a comment)
```

- [ ] **Step 6: Run `go test ./integration/ -v -race` — expect PASS**
- [ ] **Step 7: Commit** — `feat: headless client with server integration tests`

### Task 7: Server binary, TOML config, protocol doc

**Files:**
- Create: `cmd/hermec-server/main.go`, `server/config.go`, `docs/protocol.md`, `example.server.toml`
- Modify: `server/server.go` (TLS option), `README.md`
- Test: `server/config_test.go`

**Interfaces:**
- Consumes: everything prior.
- Produces:
  - `func LoadConfig(path string) (Config, error)` in `server/config.go` — TOML with keys: `addr` (default `":7697"`), `password`, `allowed_keys` (array), `channels` (array, default `["general"]`), `tls_cert`/`tls_key` (both-or-neither), `[roles]` tables mapping role→permissions plus `grants` and `default_roles` (defaults: Builtin() + `default_roles=["user"]`)
  - `cmd/hermec-server`: flags `-config <path>` (optional; defaults apply without it) and `-print-config` (dump effective config and exit); logs listen address on start; clean shutdown on SIGINT
  - `docs/protocol.md`: the versioned wire spec for everything in `core/proto` — envelope, auth flow, every message with field tables, error codes, size limit, and the two reserved E2EE types marked "reserved, v1 servers must reject" (this is the spec §3.3 "standards" deliverable for the chat subset)

- [ ] **Step 1: Write failing config tests**

```go
func TestLoadConfigDefaults(t *testing.T)  // minimal file "" → addr ":7697", channels ["general"], default_roles ["user"]
func TestLoadConfigFull(t *testing.T)      // every key set → round-trips into Config
func TestLoadConfigTLSBothOrNeither(t *testing.T) // only tls_cert set → error
```

- [ ] **Step 2: Run `go test ./server/ -v` — expect FAIL on new tests**
- [ ] **Step 3: Implement `server/config.go`, TLS listener option in `server.go`, and `cmd/hermec-server/main.go`**
- [ ] **Step 4: Run full suite `go test ./... -race` — expect PASS**
- [ ] **Step 5: Smoke test** — `go run ./cmd/hermec-server` in background; expect "listening on :7697" in output; stop it.
- [ ] **Step 6: Write `docs/protocol.md` and `example.server.toml`; update `README.md`** (what Hermec is — two paragraphs from the spec overview — plus build/run/test commands)
- [ ] **Step 7: Verify cross-compilation** — `GOOS=linux GOARCH=arm64 go build ./...` and `GOOS=windows go build ./...` both succeed.
- [ ] **Step 8: Commit** — `feat: server binary, TOML config, protocol spec v1`
