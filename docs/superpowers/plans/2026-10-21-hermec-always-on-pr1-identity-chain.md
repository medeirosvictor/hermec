# Always-on PR 1: server identity and chain — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every server gets a server ID (the hash of a founder-signed genesis entry) and a signed, append-only chain of config, grants, bans and hosts; hosts prove themselves to clients before the password is sent; protocol version 2.

**Architecture:** A new pure package `core/chain` (entries, canonical encoding, signatures, state rules, storage) is shared by server and client. The server owns one `*chain.Chain`, derives its group config from it, sends it during a two-way handshake, and accepts signed `chain_append` entries. The client verifies the chain and the host proof, pins the server ID, and keeps its copy current. The UI and `hermec-server` bootstrap and migrate chains.

**Tech Stack:** Go 1.27, `crypto/ed25519`, `crypto/sha256`, `encoding/json`, gorilla/websocket, BurntSushi/toml. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-10-21-hermec-always-on-design.md` (sections 1, 3 and 7; sections 4 and 5 are later PRs).

## Global Constraints

- Pure Go; no new modules, no cgo beyond the existing malgo.
- `proto.Version` = 2; the `challenge` frame stays a v1 envelope.
- Chain caps: at most 2048 entries and 512 KiB total (sum of encoded entry lines); every frame under `proto.MaxMessageSize` (64 KiB).
- The password never goes into the chain; it stays in TOML.
- Signature domains, exact bytes: entries `"hermec-chain-v1\x00" || canonical`; host proof `"hermec-host-proof\x00" || client_nonce || server_nonce || server_id`; client auth `"hermec-client-auth\x00" || server_nonce || client_nonce || server_id` (`server_id` = the raw 32-byte ID).
- New error codes, exact strings: `update_required`, `server_outdated`, `server_id_mismatch`, `host_not_authorized`, `bad_chain`, `chain_conflict`, `chain_full`, `banned`.
- Never run the GUI against `%APPDATA%\hermec`; never kill `hermec.exe`. Tests use temp dirs.
- Commit trailer: `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- cgo env for `go test ./...`: `C:\msys64\ucrt64\bin` on PATH, `CGO_ENABLED=1`.
- Docs describe the app as it is now: rewrite sections in place, delete stale text.

## Review Focus

1. **Tampered or reordered chain from a malicious host** (edited body, swapped entries, dropped middle entry, re-signed by a non-founder): the client must refuse with `bad_chain`, never connect. Pinned in Task 2 (`TestVerifyRejectsTampering`) and Task 5 (`TestDialRejectsTamperedChain`).
2. **A friend's saved server whose host was replaced by a different machine with a fresh chain** (same address, new genesis): the tile fails with `server_id_mismatch` and does not auto-retry. Task 5 (`TestDialPinMismatch`) and Task 7 (`TestPinMismatchNoRetry`).
3. **Old v0.5 app connecting to a v0.6 host, and a v0.6 app connecting to a v0.5 host**: both get a readable error, not a hang or "unsupported version". Task 5 (`TestV1ClientGetsUpdateRequired`, `TestDialV1HostServerOutdated`).
4. **Banning someone who is in voice right now**: they are disconnected at once (voice slot freed), and their reconnect is refused with `banned`. Task 6 (`TestBanDisconnectsLiveMember`).
5. **Restarting the app or `hermec-server` after migration**: the same server ID comes back, grants made through `chain_append` survive (TOML does not overwrite them). Task 4 (`TestOpenChainReopenKeepsAppendedGrant`) and Task 7 (`TestHostedRestartKeepsServerID`).

---

### Task 1: `core/chain` entries, canonical encoding, server ID

**Files:**
- Create: `core/chain/entry.go`, `core/chain/id.go`
- Test: `core/chain/entry_test.go`

**Interfaces:**
- Produces:
  - `type Op string` with constants `OpGenesis="genesis"`, `OpConfig="config"`, `OpGrant="grant"`, `OpRevoke="revoke"`, `OpBan="ban"`, `OpUnban="unban"`, `OpHostAdd="host_add"`, `OpHostRemove="host_remove"`.
  - `type Entry struct { Seq uint64 \`json:"seq"\`; Prev []byte \`json:"prev,omitempty"\`; Time int64 \`json:"time"\` /* unix ms */; Author []byte \`json:"author"\` /* 32-byte pubkey */; Op Op \`json:"op"\`; Body []byte \`json:"body"\` /* compact JSON, base64 on the wire, so no encoder can alter it */; Sig []byte \`json:"sig"\` }`; `func (e Entry) DecodeBody(v any) error`
  - `func (e Entry) SignedBytes() []byte` — domain prefix + compact JSON of every field except `Sig`, HTML escaping off (same as `proto.marshalNoEscape`).
  - `func (e Entry) Hash() [32]byte` — `sha256(SignedBytes() || Sig)`.
  - `func NewEntry(id *identity.Identity, prev *Entry, op Op, body any, now time.Time) (Entry, error)` — sets Seq (prev.Seq+1, or 0), Prev (prev.Hash(), or nil), Time, Author, compact Body, Sig.
  - `func (e Entry) VerifySig() bool` — signature valid and Body is compact JSON (`json.Compact(body) == body`).
  - Bodies: `GenesisBody{Name string; Founder []byte; Salt []byte}`, `ConfigBody{Name string; Channels, VoiceChannels []string; MaxVoice, MaxClients, MaxPerIP int; AllowedKeys []string; Roles map[string][]string; DefaultRoles []string}`, `GrantBody{Fingerprint string; Roles []string}`, `FPBody{Fingerprint string}` (revoke, ban, unban, host_remove), `HostAddBody{PubKey []byte; Priority int}`. JSON tags snake_case.
  - `type ID [32]byte`; `func IDOf(genesis Entry) ID` (= `genesis.Hash()`); `func (id ID) String() string` (lowercase base32, no padding, 52 chars; used for pins and filenames); `func ParseID(s string) (ID, error)`; `func (id ID) Short() string` (`"srv-"` + first 10 bytes in the `identity.Fingerprint` grouped style).

- [ ] **Step 1: Write the failing tests** in `core/chain/entry_test.go`:
  - `TestEntrySignVerify`: `NewEntry(id, nil, OpGenesis, GenesisBody{...}, t0)` → `VerifySig()` true; Seq 0, Prev nil.
  - `TestEntryTamperFails`: change Body / Time / Seq / Op after signing → `VerifySig()` false each time.
  - `TestEntryNonCompactBodyFails`: Body re-indented with spaces → false.
  - `TestEntryJSONRoundTrip`: marshal → unmarshal → `Hash()` equal, `VerifySig()` true.
  - `TestIDStringRoundTrip`: `ParseID(id.String()) == id`; `len(id.String()) == 52`; `strings.HasPrefix(id.Short(), "srv-")`.
  - `TestNextEntryLinks`: second entry has Seq 1 and Prev == first.Hash().
- [ ] **Step 2:** `go test ./core/chain/` → FAIL (package missing).
- [ ] **Step 3:** Implement `entry.go` and `id.go` per the interfaces.
- [ ] **Step 4:** `go test ./core/chain/` → PASS.
- [ ] **Step 5:** Commit `feat(chain): signed entries and server ID`.

### Task 2: `core/chain` state rules and chain storage

**Files:**
- Create: `core/chain/state.go`, `core/chain/chain.go`
- Test: `core/chain/state_test.go`, `core/chain/chain_test.go`, `core/chain/fuzz_test.go`

**Interfaces:**
- Consumes: Task 1.
- Produces:
  - `type Host struct { PubKey ed25519.PublicKey; Fingerprint string; Priority int }`
  - `type State struct { ID ID; Founder string /*fp*/; Config ConfigBody; HasConfig bool; Grants map[string][]string; Bans map[string]bool; Hosts []Host /*sorted by Priority, then Fingerprint*/ }`
  - `func (s *State) Apply(e Entry) error` — checks authority against the state before the entry and applies it. Errors are `*RuleError{Reason string}`.
  - `func (s *State) Can(fp string, op Op) bool` — founder can do all; `config/grant/revoke/ban/unban` need `roles.PermManage` via `s.Roles()`; `host_add/host_remove` founder only; `genesis` only at Seq 0.
  - `func (s *State) Roles() roles.Config` — builtin roles + `Config.Roles` overrides, `Grants`, `DefaultRoles` (`["user"]` when no config).
  - `func (s *State) IsHost(pub ed25519.PublicKey) bool`, `func (s *State) Banned(fp string) bool`.
  - `type Chain struct` (mutex inside): `func New(genesis Entry) (*Chain, error)`, `func Load(path string) (*Chain, error)`, `func FromEntries(es []Entry) (*Chain, error)`, `func (c *Chain) Append(e Entry) error` (validates links, signature, rule, caps; persists one JSON line when a path is set), `func (c *Chain) SetPath(path string) error` (writes the whole file atomically, then appends lines), `func (c *Chain) Head() Entry`, `func (c *Chain) Entries() []Entry` (copy), `func (c *Chain) Since(seq uint64) []Entry` (entries with Seq > seq), `func (c *Chain) State() State` (copy), `func (c *Chain) ID() ID`.
  - Errors: `ErrConflict` (Seq/Prev not the head's successor), `ErrFull` (over 2048 entries or 512 KiB), `ErrBadChain` (signature, link or rule failure while loading or verifying).
- Rules (exact): genesis author must equal `GenesisBody.Founder` and `Seq == 0`; nobody can `revoke` or `ban` the founder; `host_add` with a pubkey already present updates its priority; `host_remove` of an unknown fp is a rule error; `Priority >= 1`.

- [ ] **Step 1: Write the failing tests**:
  - `TestFounderCanDoEverything`, `TestAdminCanBanNotAddHost` (grant admin to B; B's `ban` ok; B's `host_add` → RuleError), `TestUserCannotGrant`, `TestFounderCannotBeRevokedOrBanned`, `TestConfigLatestWins`, `TestHostsSortedByPriority`.
  - `TestAppendConflict` (two entries built on the same head: second → `ErrConflict`), `TestAppendFull` (caps live in package vars `maxEntries = 2048`, `maxBytes = 512 << 10`; the test lowers `maxEntries` to 3 and the 4th append → `ErrFull`), `TestLoadRoundTrip` (SetPath, append 3, `Load` → same `ID()` and `Head().Hash()`).
  - `TestVerifyRejectsTampering`: write a chain file, then for each of edited body / swapped lines / dropped middle line / entry re-signed by a non-founder → `Load` returns `ErrBadChain`.
  - `FuzzFromEntries` in `fuzz_test.go`: arbitrary JSON lines never panic.
- [ ] **Step 2:** `go test ./core/chain/` → FAIL.
- [ ] **Step 3:** Implement `state.go` and `chain.go`. `Load` replays every line through `FromEntries` (full verify).
- [ ] **Step 4:** `go test ./core/chain/ && go test -run=^$ -fuzz=FuzzFromEntries -fuzztime=20s ./core/chain/` → PASS.
- [ ] **Step 5:** Add the fuzz target to the `fuzz` job in `.github/workflows/ci.yml` next to the existing proto fuzz target. Commit `feat(chain): state rules, caps and storage`.

### Task 3: protocol version 2 messages

**Files:**
- Modify: `core/proto/proto.go`
- Test: `core/proto/proto_test.go`

**Interfaces:**
- Produces:
  - `Version = 2`, `LegacyVersion = 1`.
  - `func EncodeV(v int, msgType string, payload any) ([]byte, error)`; `Encode` = `EncodeV(Version, …)`.
  - `func DecodeHandshake(raw []byte) (Envelope, error)` — like `Decode` but accepts V 1 or 2.
  - Types: `TypeHostProofReq="host_proof_req"`, `TypeHostProof="host_proof"`, `TypeChain="chain"`, `TypeChainAppend="chain_append"`.
  - Structs: `HostProofReq{Nonce []byte}`, `HostProof{PubKey []byte; Sig []byte}`, `ChainFrame{Entries []json.RawMessage \`json:"entries"\`; More bool \`json:"more,omitempty"\`}` (raw entries so `proto` does not import `chain`), `ChainAppend{Entry json.RawMessage}`; `AuthOK.ServerID string \`json:"server_id"\`` (the `chain.ID.String()` form).
  - Code constants for every new code in Global Constraints (`CodeUpdateRequired`, `CodeServerOutdated`, `CodeServerIDMismatch`, `CodeHostNotAuthorized`, `CodeBadChain`, `CodeChainConflict`, `CodeChainFull`, `CodeBanned`); update the `ErrorMsg` code list comment.
  - Delete the "Screen sharing (additive; protocol Version stays 1)" comment wording that no longer holds.

- [ ] **Step 1:** Tests: `TestDecodeRejectsV1`, `TestDecodeHandshakeAcceptsV1AndV2`, `TestEncodeVLegacy` (envelope `v` is 1), `TestChainFrameRoundTrip`.
- [ ] **Step 2:** `go test ./core/proto/` → FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** `go test ./core/proto/` → PASS (the existing fuzz seeds still pass).
- [ ] **Step 5:** Commit `feat(proto): version 2 handshake and chain messages`.

### Task 4: server chain bootstrap, migration and effective config

**Files:**
- Create: `server/chain.go`
- Modify: `server/server.go` (Config fields, `New`), `server/config.go` (nothing new in TOML except what follows), `cmd/hermec-server/main.go`
- Test: `server/chain_test.go`

**Interfaces:**
- Consumes: Tasks 1–2.
- Produces:
  - `Config.HostKey *identity.Identity` (the host's own key; nil → `New` generates an ephemeral one), `Config.Chain *chain.Chain` (nil → `New` builds an in-memory chain from the TOML group fields, founder = HostKey, so every existing test keeps working).
  - `func OpenChain(path string, key *identity.Identity, cfg Config) (c *chain.Chain, created bool, err error)` — file missing: write genesis (name, founder = key, 16 random salt bytes), one `config` from cfg's group fields, `grant` per `cfg.Roles.Grants` entry (sorted by fp), `host_add {key, 1}`, save at `path`; file present: `chain.Load` and return.
  - `func ApplyTOML(c *chain.Chain, key *identity.Identity, cfg Config) (n int, err error)` — appends a `config` entry if the group fields differ from the state and `grant`/`revoke` entries for grant differences; refuses (RuleError) when key lacks authority. Used only by `-apply-config` and by `OpenChain` creation.
  - `func GroupFieldsSet(path string) ([]string, error)` — which group keys a TOML file sets (`server_name`, `channels`, `voice_channels`, `max_voice`, `max_clients`, `max_per_ip`, `allowed_keys`, `roles`, `default_roles`, `grants`), for the "ignored" warning.
  - `New` derives `ServerName`, `Channels`, `VoiceChannels`, `MaxVoice`, `MaxClients`, `MaxPerIP`, `AllowedKeys`, and roles from `Config.Chain.State()`; machine-local fields (Addr, Password, TLS, PublicIP, ports, MediaPort, msg rate, Discoverable, Logf, test flags) still come from `Config`. The server reads roles and bans through one accessor, so Task 6 can update them live.
  - `hermec-server` flags: `-key` (default `hermec-server.key` next to `-config`, or in the working dir without one; generated with `identity.Generate` + `Save` when missing), `-chain` (default `chain.jsonl` next to the config / working dir), `-apply-config` (open the chain, `ApplyTOML`, print how many entries were appended, exit). On normal start, log the server ID (`Short()`) and, when the chain already existed, warn once naming the group keys from `GroupFieldsSet` that are ignored.

- [ ] **Step 1: Write the failing tests** in `server/chain_test.go`:
  - `TestOpenChainCreatesFromConfig`: cfg with name "primos", channels, a grant → created true; state name/channels/grants match; host list = [key at 1]; founder = key's fp.
  - `TestOpenChainReopenKeepsAppendedGrant`: create, append a signed `grant` for X with the founder key, reopen with the original cfg → X's grant still there; ID unchanged.
  - `TestApplyTOMLAppendsDiffOnly`: changing only `channels` appends exactly one `config` entry; same cfg again → 0.
  - `TestApplyTOMLNoAuthority`: a key that is not founder/admin → RuleError, chain unchanged.
  - `TestNewDerivesGroupConfigFromChain`: chain says name "b", cfg says "a" → `auth_ok.server_name` "b" (dial with a raw ws helper from existing tests, or assert through an exported accessor if one exists).
  - `TestNewWithoutChainStillWorks`: `New(DefaultConfig())` starts and serves (existing tests cover this; one explicit check is enough).
- [ ] **Step 2:** `go test ./server/ -run 'Chain|ApplyTOML|Derives'` → FAIL.
- [ ] **Step 3:** Implement `server/chain.go`, wire `New`, then the `hermec-server` flags.
- [ ] **Step 4:** `go test ./server/ ./cmd/...` → PASS.
- [ ] **Step 5:** Commit `feat(server): chain bootstrap, migration and -apply-config`.

### Task 5: version 2 handshake (server and client)

**Files:**
- Modify: `server/conn.go` (`authenticate`), `client/client.go` (`Dial`, `handshake`, `Client` fields)
- Create: `client/chain.go` (client-side verify helpers)
- Test: `server/auth_test.go`, `client/client_test.go`, `client/handshake_test.go`

**Interfaces:**
- Consumes: Tasks 1–4.
- Produces:
  - Server flow exactly as spec 3.3. Pre-auth bucket allows two inbound frames. `authenticate` peeks the first reply with `proto.DecodeHandshake`: V 1 → send `EncodeV(LegacyVersion, error {update_required, "update HERMEC to connect to this server"})` and close (count it as an auth failure only if the frame is not a well-formed v1 `auth`). Chain frames: entries in order, each frame under 60 KiB of entries, `More` on all but the last. Banned fp → `error {banned, "you are banned from this server"}` (not `noteAuthFailure`). Client sig verified over the client-auth domain bytes.
  - `func DialPinned(ctx context.Context, url string, id *identity.Identity, name, password string, pin string) (*Client, error)` — `pin` is a `chain.ID.String()` or "" (trust on first use). `Dial(...)` = `DialPinned(..., "")`.
  - Client errors (as `*ServerError`, so the UI's `errors.As` works): `server_id_mismatch`, `host_not_authorized`, `bad_chain`, `server_outdated` (the v1 host answered `host_proof_req` with an error or anything but `chain`/`host_proof`).
  - `func (c *Client) ServerID() string`, `func (c *Client) Chain() *chain.Chain` (the client's live copy).

- [ ] **Step 1: Write the failing tests**:
  - server: `TestV1ClientGetsUpdateRequired` (raw ws: read challenge, send a v1 `auth` → one v1 `error` with code `update_required`), `TestBannedKeyRefused` (chain with a ban → `banned`), `TestClientSigBoundToServerID` (sig over the old raw-nonce bytes → `auth_failed`).
  - client: `TestDialPinsAndReportsServerID` (Dial → `ServerID()` == server chain ID), `TestDialPinMismatch` (`DialPinned` with another ID → `server_id_mismatch`, and the password never reached the server: assert with a test server hook or a fake host that records `auth`), `TestDialHostNotAuthorized` (fake host signs the proof with a key not in the host list), `TestDialRejectsTamperedChain` (fake host sends an edited entry → `bad_chain`), `TestDialV1HostServerOutdated` (fake v1 host that answers `host_proof_req` with `auth_failed` → `server_outdated`), `TestDialLargeChainChunked` (chain of ~1500 entries crosses several frames and verifies).
  - The fake host lives in `client/handshake_test.go` as a small ws handler built on `core/chain` and `core/proto`.
- [ ] **Step 2:** `go test ./server/ ./client/ -run 'V1|Banned|SigBound|Dial'` → FAIL.
- [ ] **Step 3:** Implement server, then client.
- [ ] **Step 4:** `go test ./core/... ./server/ ./client/ ./integration/` → PASS. `integration/compat_test.go` tests that emulate a pre-v0.5 host (`LegacyHost`) can no longer connect at all: delete those tests and the `LegacyHost` config field with its branches in `server/` (keep `NoVP8` if a remaining test uses it, else delete it too). Note the deletion in the commit message.
- [ ] **Step 5:** Commit `feat: protocol v2 mutual handshake with chain proof`.

### Task 6: live chain updates, `chain_append`, bans

**Files:**
- Modify: `server/conn.go` (route), `server/chain.go`, `client/client.go` (readLoop, new method), `client/chain.go`
- Test: `server/chainappend_test.go`, `client/chain_test.go`

**Interfaces:**
- Consumes: Tasks 1–5.
- Produces:
  - Server: on `chain_append` it decodes `chain.Entry`, requires `Author` == the connection's pubkey, calls `Chain.Append`; errors map to `chain_conflict`, `chain_full`, `forbidden` (RuleError, message = reason), `bad_request`. On success it broadcasts `chain {entries:[e]}` to every authenticated conn, then applies effects: `ban` → every conn with that fp gets `error {banned}` and is finished (voice left); `grant`/`revoke`/`config` roles and bans take effect for the next permission check; name, channels and limits from a later `config` apply at the next start (documented in protocol.md).
  - Client: readLoop applies `chain` frames to `c.Chain()` (an entry that fails to append → close the connection with `bad_chain` as the terminal event); `Event.Chain *chain.Entry` emitted for each new entry.
  - `func (c *Client) ChainAppend(ctx context.Context, op chain.Op, body any) error` — builds the entry on the local head with the client's identity, sends it, waits for its own entry to come back in a `chain` frame or an error (reuse the join-waiter pattern); one retry on `chain_conflict` after the local copy catches up.

- [ ] **Step 1: Write the failing tests**:
  - `TestChainAppendBroadcasts` (admin appends a grant; a second client sees `Event.Chain` with it).
  - `TestChainAppendForbiddenForUser`, `TestChainAppendAuthorMismatch`.
  - `TestBanDisconnectsLiveMember` (B in voice; founder bans B → B gets `banned` and its connection ends; voice_state no longer lists B; B's redial → `banned`).
  - `TestChainAppendConflictRetries` (two admins append at once; both succeed in some order).
- [ ] **Step 2:** `go test ./server/ ./client/ -run 'ChainAppend|Ban'` → FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** `go test -race ./server/ ./client/` → PASS.
- [ ] **Step 5:** Commit `feat: chain_append, live chain updates and bans`.

### Task 7: app: hosted chain, migration and server ID pins

**Files:**
- Modify: `ui/hosted.go`, `ui/session.go` (`dial`, `applyDial`), `ui/rail.go` (`saveServer`), `ui/state/servers.go`, `ui/state/errors.go` or wherever `DialErrorText` lives
- Test: `ui/hosted_test.go`, `ui/state/servers_test.go`, `ui/authfail_test.go`

**Interfaces:**
- Consumes: Tasks 4–6.
- Produces:
  - `hosted.toml` gains `server_id` (machine-local field in `fileConfig`/`Config`: `ServerID string \`toml:"server_id"\``). Chain file: `<config>/hermec/chains/<server_id>.jsonl`.
  - `createHosted`: `OpenChain` into a temp path, then rename to `chains/<id>.jsonl`, save hosted.toml with `server_id` and only machine-local fields + password.
  - `bootHosted` migration: hosted.toml without `server_id` → `OpenChain` from its full config (founder = `g.id`), then rewrite it without group fields and with `server_id`. With `server_id` → `chain.Load`. Pass `HostKey: g.id`, `Chain: c` to `server.New`.
  - Code that read group fields from `g.hosted.cfg` (name for the invite and tile, voice channels) reads them from the chain state instead.
  - `ServerEntry.ServerID string \`toml:"server_id,omitempty"\``; `func PinID(entries []ServerEntry, url, id string) []ServerEntry` (sets it only when empty); `dial` passes the entry's pin (the hosted session's pin = hosted `server_id`) to `client.DialPinned`; `saveServer` records `c.ServerID()` through `PinID`.
  - The rail tile's right-click menu gets an info row `id srv-…` (`chain.ID.Short()`); clicking it copies the full ID (same clipboard path as the invite copy). Hidden until the server ID is known.
  - `applyDial`: `server_id_mismatch`, `host_not_authorized`, `bad_chain`, `server_outdated`, `update_required` and `banned` stop automatic retry like `auth_failed` (the ↻ still works). `DialErrorText` gives each a plain message: "this server's identity changed — remove and re-add it if you trust it", "this machine is not an approved host for the server", "the server sent an invalid chain", "the host runs an older HERMEC — ask them to update", "update HERMEC to connect to this server", "you are banned from this server".

- [ ] **Step 1: Write the failing tests**:
  - `TestHostedMigrationCreatesChain` (old-style hosted.toml in a temp config dir → chain file exists, hosted.toml has `server_id`, no `server_name`/`channels`/`grants` keys, founder = test key).
  - `TestHostedRestartKeepsServerID` (boot twice → same ID, chain not rewritten).
  - `TestPinIDOnlyWhenEmpty`, `TestServersTOMLKeepsServerID` (round trip).
  - `TestPinMismatchNoRetry` (dial result with `server_id_mismatch` → `retryAt` zero, `lastErr` has the plain text); table over the other five codes.
- [ ] **Step 2:** `go test ./ui/... -run 'Hosted|PinID|ServerID|NoRetry'` → FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** `go test ./ui/...` → PASS.
- [ ] **Step 5:** Commit `feat(ui): hosted chain, migration and server ID pins`.

### Task 8: integration test and docs

**Files:**
- Create: `integration/chain_test.go`
- Modify: `docs/protocol.md`, `README.md`, `HANDOFF.md`, `example.server.toml` (if it documents group fields, add the note about `-apply-config`)

- [ ] **Step 1:** `integration/chain_test.go`: `TestHostedMigrationEndToEnd` — a server started from an old-style config via `OpenChain`; client A (founder) and client B connect with `DialPinned("")`, both report the same `ServerID()`; A grants B admin with `ChainAppend`; B bans C; C's dial → `banned`; restart the server from the same chain file → A redials with its pin and succeeds.
- [ ] **Step 2:** `go test ./integration/ -run Chain` → PASS (write it against the finished pieces; if it fails, fix the owning task's code).
- [ ] **Step 3: Docs, rewritten in place:**
  - `docs/protocol.md`: section 3 handshake becomes the six-step v2 flow with the exact signature byte strings; new messages `host_proof_req`, `host_proof`, `chain`, `chain_append`; `auth_ok.server_id`; the new error codes; a chain section (entry fields, ops and who may sign them, caps, canonical encoding); version 2 compatibility rules (section 7) replacing the v1 text.
  - `README.md`: "How safe is this?" — the host proves it is an approved host, the app pins the server ID and warns when it changes, bans; hosting section — `hermec-server -key/-chain/-apply-config` and the ignored-group-fields rule; note that everyone in a group needs v0.6.
  - `HANDOFF.md`: PR 1 done line in "Current round"; remove "auth domain separation" and "auth bound to server identity" from the deferred and ideas lists (both done here).
- [ ] **Step 4:** `go vet ./... && go test ./...` → PASS; `gofmt -l` on changed files → empty (ignore CRLF-only noise).
- [ ] **Step 5:** Commit `test(integration): chain end to end; docs for protocol v2`.
