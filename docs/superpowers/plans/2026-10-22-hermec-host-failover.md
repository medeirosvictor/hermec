# Host link, warm standby and failover Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A server keeps running while any granted host has HERMEC open. Losing the active host mid-call costs about 1 to 1.5 s of audio, with no reconnect screen and no clicks.

**Architecture:** Every host runs a normal `server.Server` in one of three modes: active, standby or idle. A new `server.Host` orchestrator owns the mode. It runs cold start, the host link to the active host, promotion, handback and split-brain. Non-active hosts mirror the chain, chat history and password over the host link. Clients hold a paused ("warm") session on the standby, plus a data-channel-only WebRTC peer while in voice. When the active host goes quiet they send `takeover` there and their call moves to that peer. The UI gains friend hosting, roster host controls, known host addresses and "hosted by".

**Tech Stack:** Go, gorilla/websocket, pion/webrtc v4 (data channels), Ebiten UI, existing `core/chain` and `core/proto`.

**Spec:** `docs/superpowers/specs/2026-10-21-hermec-always-on-design.md`, section 5 (plus 3.2 password sync, 4.3 README text, 6 testing, 7 docs). Victor chose one PR for all of section 5 (2026-10-22).

## Global Constraints

- Pure Go, no new C dependencies.
- Nothing may block external hosting later. The headless `hermec-server` is a full host peer (5.8).
- `proto.Version` stays 2. New message types are additive: clients ignore unknown types, and v0.6 clients keep working against a v0.7 single host.
- Every frame stays at most `proto.MaxMessageSize` (64 KiB). The chain and history replicate in frames under 60 KiB.
- Heartbeats every 500 ms in both directions on the host link, and from the active host to every client. A client declares the active host lost after 1.5 s without a heartbeat.
- Cold start tries known hosts for about 3 s. An active host probes the other known hosts every 30 s. Handback happens after 2 minutes of empty voice.
- Priority is the chain host-list order (`State.Hosts`, priority 1 is highest, founder first).
- Split-brain yield order: fewer voice members yields; ties: fewer clients yields; then the lower-priority host (larger number) yields.
- Exact user-facing copy:
  - system line `host changed to <name>`
  - chain-loss notice `your change was lost during a network split; please redo it`
  - voice panel `hosted by <name>`
  - toggle label `let my PC host this server`
  - roster items `Allow hosting`, `Stop hosting`, `Move up`
- Agents never run the GUI against `%APPDATA%\hermec` and never kill `hermec.exe`. UI work is tested headless with `newTestGame`.
- cgo env: PATH includes `C:\msys64\ucrt64\bin`, `CGO_ENABLED=1`.
- Commit trailer: `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Docs describe the app as it is now. Rewrite sections in place.

## Decisions this plan adds (spec gaps; Victor reviews these with the plan)

1. **Roster carries public keys.** `proto.Member` gains `PubKey []byte "pub_key,omitempty"`, so the founder can build `host_add` from a roster row.
2. **"Let my PC host this server" lives in the rail tile's right-click menu.** It is a per-server toggle that persists. Settings rows are global, so the spec's "Settings" maps to the tile menu. The toggle is on by default once the chain lists you as a host.
3. **Friend hosts' files.**
   - A friend host keeps `<config>/hermec/hosting/<server-id>.toml` (machine-local: port, password, known host addresses).
   - It reuses `chains/<server-id>.jsonl` and `history/<server-id>.jsonl`.
   - Its port defaults to the first free port from 7697 to 7707 and is saved once chosen.
   - The founder's `hosted.toml` stays as it is.
4. **Advertised addresses.**
   - A host advertises `ws://<ip>:<port>/` for its tailnet IPs and LAN IPs, using the UI's existing `selfAddrs` seam.
   - `hermec-server` takes `-advertise` (comma list) and `-hosts` (comma list of known host URLs to link to).
5. **The password never travels (Victor, 2026-10-22).**
   - Hosts keep `K = scrypt(password, server_id)` (N=32768, r=8, p=1, 32 bytes; `golang.org/x/crypto/scrypt`, pure Go) instead of the password. The password is gone from host TOML; `hosted.toml`/`hosting/*.toml` store `password_key` (hex).
   - The client sends `Auth.Proof = HMAC-SHA256(K, "hermec-pw\0" || server_nonce || client_nonce || server_id)` in place of `Auth.Password`.
   - The client caches K per server in `servers.toml` (`password_key`) after the first successful login, so scrypt runs once.
   - Hosts share K in `link_sync{PasswordKey}`, never the password.
   - Compatibility: a host still accepts a plain `Auth.Password` from v0.6 clients (it derives K and compares) for this release. Docs say plain passwords are refused from v0.8.
   - The README says that every host holds the server's login key.
5b. **Reachability (Victor, 2026-10-22: option a, node sharing).**
   - Each friend host must node-share its PC with the group on Tailscale.
   - Clients report whether they reached each host's warm session: `reach{fingerprint, ok}`.
   - The active host picks as standby the highest-priority linked host that at least half of the connected clients reach. If none qualifies, there is no standby, and failover falls back to the reconnect loop.
   - The tile-menu toggle shows a one-time notice when switched on: `friends must be able to reach this PC (share it on Tailscale)`.
6. **Non-active host refusing a normal client.**
   - The handshake runs as usual (the chain proof is still useful).
   - Then a full `auth` gets `hosts {…}` followed by error `not_active`, and the connection closes.
   - The client saves the host list and dials the active host at once, without backoff.
7. **Error correlation (PR 1 open item).** `ChainAppend` gains `Ref string`. `ErrorMsg` gains `Ref string "ref,omitempty"`, echoed back, and the client matches chain errors by `Ref`, not FIFO.

## Review Focus

1. **A client-only network blip must not fail over.** A client whose own connection drops gets `active_alive` and reconnects to the same active host. It must never promote the standby while the active host is alive. Test: Task 7 `TestTakeoverActiveAlive`, Task 15 `TestClientBlipNoFailover`.
2. **Standby lost before the active host.** When the standby dies first, clients drop the old warm session and open one to the new standby. A later active-host death still fails over. Test: Task 15 `TestStandbyReplacedThenFailover`.
3. **Two takeovers race.** Many clients send `takeover` within the same 100 ms. Promotion runs once, and every client gets a full session. Test: Task 7 `TestTakeoverConcurrent`.
4. **Chain rollback.** A host serving an older valid prefix than the client has seen is refused with `bad_chain`, for both host links and client tiles (PR 1 open item). Test: Task 10 `TestRollbackRefused`, Task 4 `TestLinkRefusesOlderChain`.
5. **Bans and revokes during failover.** A member banned on the active host is still banned on the promoted standby. A host removed with `host_remove` drops its link and can no longer take over. Test: Task 5 `TestLinkReplicatesBan`, Task 7 `TestRemovedHostCannotPromote`.

---

### Task 1: Protocol additions

**Files:**
- Modify: `core/proto/proto.go`
- Modify: `core/proto/handshake_sig.go`
- Test: `core/proto/proto_test.go`

**Interfaces:**
- Produces, all in `core/proto`:
  - Type constants:
    - `TypeHosts="hosts"`
    - `TypeHeartbeat="hb"`
    - `TypeTakeover="takeover"`
    - `TypeMove="move"`
    - `TypeLinkHello="link_hello"`
    - `TypeLinkSync="link_sync"`
    - `TypeLinkRole="link_role"`
    - `TypeLinkProbe="link_probe"`
    - `TypeLinkProbeReply="link_probe_reply"`
    - `TypeHandover="handover"`
    - `TypeWarmOffer="warm_offer"`
    - `TypeWarmAnswer="warm_answer"`
    - `TypeWarmCandidate="warm_candidate"`
  - Codes:
    - `CodeNotActive="not_active"`
    - `CodeActiveAlive="active_alive"`
    - `CodeNotStandby="not_standby"`
    - `CodeChainLost="chain_lost"`
    - `CodeForbidden="forbidden"`
    - `CodeBadRequest="bad_request"`
  - Structs:
    - `HostInfo{Fingerprint, Name string; Addrs []string; Priority int}`
    - `Hosts{Active HostInfo; Standby *HostInfo "standby,omitempty"}`
    - `Heartbeat{Seq uint64}`
    - `Move{To HostInfo; Reason string}` with reason `"handback"` or `"merge"`
    - `LinkHello{ChainSeq uint64 "chain_seq"; HistoryLastID string "history_last_id"; HistoryLastTS time.Time "history_last_ts"; Addrs []string; Name string}`
    - `LinkSync{Password string "password,omitempty"; Messages []ChatMessage "messages,omitempty"; More bool}`
    - `LinkRole{Standby bool}`
    - `LinkProbeReply{Active bool; ServerID string "server_id"; VoiceMembers, Clients, Priority int; Self HostInfo}`
    - `Handover{}`
  - Changes to existing types:
    - `Auth` gains `Warm bool "warm,omitempty"`.
    - `Member` gains `PubKey []byte "pub_key,omitempty"`.
    - `ChainAppend` gains `Ref string "ref,omitempty"`.
    - `ErrorMsg` gains `Ref string "ref,omitempty"`.
  - `ChatMessage` gets a `System bool "system,omitempty"` flag for host-change lines.
  - `WarmOffer`, `WarmAnswer` and `WarmCandidate` reuse the shapes of `RTCOffer`/`RTCAnswer`/`RTCCandidate` (alias types).

- [ ] **Step 1: Write the failing test**

```go
func TestPR3RoundTrip(t *testing.T) {
	for _, tc := range []struct{ typ string; v any }{
		{TypeHosts, Hosts{Active: HostInfo{Fingerprint: "ab", Name: "vic", Addrs: []string{"ws://1.2.3.4:7697/"}, Priority: 1}}},
		{TypeLinkHello, LinkHello{ChainSeq: 7, HistoryLastID: "x", Addrs: []string{"ws://a/"}}},
		{TypeLinkProbeReply, LinkProbeReply{Active: true, VoiceMembers: 3, Clients: 5, Priority: 2}},
		{TypeMove, Move{To: HostInfo{Fingerprint: "cd"}, Reason: "handback"}},
	} {
		raw, err := Encode(tc.typ, tc.v)
		if err != nil { t.Fatal(err) }
		env, err := Decode(raw)
		if err != nil || env.Type != tc.typ || env.V != Version { t.Fatalf("%s: %v %+v", tc.typ, err, env) }
	}
}
func TestAuthWarmOmittedWhenFalse(t *testing.T) {
	raw, _ := json.Marshal(Auth{Name: "a"})
	if bytes.Contains(raw, []byte("warm")) { t.Fatal("warm leaked") }
}
```

- [ ] **Step 2: Run** `go test ./core/proto -run 'PR3|Warm'`. Expected: FAIL (undefined types).
- [ ] **Step 3: Add the constants and types above to `core/proto/proto.go`.** Replace the raw `"forbidden"` and `"bad_request"` strings across `server/` with the new constants (mechanical).
- [ ] **Step 4: Run** `go test ./core/proto ./server`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(proto): host link, failover and warm session messages`.

### Task 1b: Password key login

**Files:**
- Create: `core/proto/pwkey.go`
- Modify: `server/conn.go` (`authenticate`), `server/config.go` (`password_key`), `client/client.go` (handshake), `ui/state/servers.go` (`PasswordKey`), `ui/hosted.go` (migration)
- Test: `core/proto/pwkey_test.go`, `server/auth_test.go`, `client/handshake_test.go`

**Interfaces:**
- Produces:
  - In `core/proto`:
    - `PasswordKey(password string, serverID []byte) ([]byte, error)`: scrypt with N=32768, r=8, p=1, 32 bytes, salt `"hermec-pw\0" || server_id`.
    - `PasswordProof(key, serverNonce, clientNonce, serverID []byte) []byte`.
    - `Auth.Proof []byte "proof,omitempty"`.
  - `Config.PasswordKey []byte`. `Config.Password` is still read once from an old TOML, then migrated: derive K, write `password_key`, drop `password`.
  - Server check:
    - With `Proof`, compare it against `PasswordProof(K, ...)` using `hmac.Equal`.
    - With `Password` (v0.6 clients), derive K and compare in constant time.
    - With neither, when K is set, return `auth_failed`.
  - `client.DialOpts` gains `PasswordKey []byte`. With a key, the client sends `Proof`. Without one, it derives K from the password and exposes `(c *Client) PasswordKey() []byte` for the UI to cache.

- [ ] **Step 1: Write the failing tests.**
  - `TestPasswordKeyDeterministic`: same input gives the same key. A different server ID gives a different key.
  - `TestAuthWithProof`: a server with K, and a client with the right password, succeeds. A wrong password gets `auth_failed`. Capture the client's `auth` frame and assert it contains no `"password"`.
  - `TestAuthLegacyPassword`: a raw v2 `auth` with `Password` still succeeds.
  - `TestHostedTOMLMigratesPassword`: an old `hosted.toml` with `password = "pw"` boots, then holds `password_key` and no `password`.
- [ ] **Step 2: Run** `go test ./core/proto ./server ./client -run 'PasswordKey|Proof|Legacy|Migrates'`. Expected: FAIL.
- [ ] **Step 3: Implement.** Add `golang.org/x/crypto` if it is not already in `go.mod`.
- [ ] **Step 4: Run** `go test -race ./core/proto ./server ./client ./ui/...`. Expected: PASS.
- [ ] **Step 5: Commit** `feat: password key login; the password never goes over the wire`.

### Task 2: History export and merge

**Files:**
- Modify: `server/history.go`
- Test: `server/history_test.go`

**Interfaces:**
- Produces:
  - `(h *History) After(ts time.Time, id string, max int) (msgs []proto.ChatMessage, more bool)`: all channels, ordered by `(TS, ID)`, strictly after `(ts, id)`. A zero `ts` means from the start.
  - `(h *History) Merge(msgs []proto.ChatMessage) (added int, err error)`: inserts unknown ids, keeps `(TS, ID)` order, persists, then enforces caps the same way `Add` does.
  - `(h *History) Last() (ts time.Time, id string)`.

- [ ] **Step 1: Write the failing tests.** Each runs on a temp-file store with three channels.
  - `TestHistoryAfterOrdersAcrossChannels`: add 5 messages across channels with increasing TS. `After(zero,"",100)` returns all 5 in `(TS,ID)` order. `After(ts2,id2,100)` returns messages 3 to 5. `After(zero,"",2)` returns 2 with `more==true`.
  - `TestHistoryMergeDedup`: store A has m1 and m2. `Merge([m2,m3,m0])` returns `added==2`. `Page` shows m0, m1, m2, m3 in order, and reopening the file shows the same.
  - `TestHistoryMergeRespectsCaps`: limits of 1 MB. Merging 2 MB of messages leaves `Size() <= 1<<20`.
- [ ] **Step 2: Run** `go test ./server -run History`. Expected: FAIL.
- [ ] **Step 3: Implement.** `After` walks per-channel slices with a k-way merge by `(TS,ID)`. `Merge` reuses `Add`'s insert and persist path under one lock hold.
- [ ] **Step 4: Run** `go test -race ./server -run History`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(server): history export after a point and merge by id`.

### Task 3: Server modes, warm sessions and heartbeats

**Files:**
- Create: `server/mode.go`
- Modify: `server/server.go` (Server fields, `New`)
- Modify: `server/conn.go` (`authenticate`, `joinLive`, the `warm` field)
- Modify: `server/voice.go` (`markAuthed`)
- Modify: `server/channels.go` (keepalive)
- Test: `server/mode_test.go`

**Interfaces:**
- Produces:
  - `type Mode int` with `ModeActive`, `ModeStandby`, `ModeIdle`.
  - `Config.Mode Mode`. The zero value is `ModeActive`, so existing callers are unchanged.
  - `(s *Server) Mode() Mode` and `(s *Server) SetMode(m Mode)`.
  - Package vars `heartbeatInterval = 500 * time.Millisecond` and `heartbeatGrace = 1500 * time.Millisecond`. They are overridable in tests.
  - `conn.warm bool`: set when `Auth.Warm`.
  - `s.warm map[*conn]struct{}`, guarded by `chMu`.
  - Warm conns get chain frames and `hb`, and nothing else. They are not in `authed` or presence, and are not counted by `reserveClient`. Each IP is still counted by `admit`.
  - `(s *Server) promoteWarmLocked() []*conn`: moves every warm conn into `authed` via `markAuthed`.
  - The active host sends `hb{Seq}` every `heartbeatInterval` to authed and warm conns. A standby or idle host sends `hb` to warm conns too, so a client can tell whether the standby is alive.
  - In a non-active mode, a full (non-warm) `auth` gets `sendMsg(TypeHosts, s.hostsInfo())`, then `sendError(CodeNotActive, "this host is on standby")`, then close.
  - `s.hostsInfo()` is set by the orchestrator through `(s *Server) SetHosts(h proto.Hosts)`.
  - `Member.PubKey` is filled wherever the server builds a `proto.Member` from a conn.

- [ ] **Step 1: Write the failing tests** (in-package, using the existing `startServer` helper pattern from `server/auth_test.go`).
  - `TestWarmSessionGetsNoPresence`: a full client joins `general`, then a warm client authenticates. The full client sees no roster change. The warm client gets an `hb` within 1 s and no `roster`, `presence` or `chat_message`.
  - `TestWarmNotCountedAsClient`: with `MaxClients: 1`, one full client and one warm client both connect.
  - `TestStandbyRefusesFullAuth`: `Config{Mode: ModeStandby}`. A full auth reads `hosts`, then error `not_active`, then close.
  - `TestPromoteWarm`: on a standby with one warm conn, `SetMode(ModeActive)` and `promoteWarmLocked()`. The conn now receives `roster` and can `join` and `chat`.
  - `TestRosterHasPubKey`: roster members carry `PubKey` equal to the client's key.
- [ ] **Step 2: Run** `go test ./server -run 'Warm|Standby|Promote|PubKey'`. Expected: FAIL.
- [ ] **Step 3: Implement the mode field and warm handling.**
  - In `joinLive`, call `markAuthed` only for non-warm conns in active mode. Otherwise add the conn to `s.warm`.
  - Extend chain broadcasts (`handleChainAppend` and link-applied entries) to warm conns.
  - Drop the conn from `s.warm` in `leaveAll`.
  - In a non-active mode, `route` answers everything except `takeover`, `link_probe` and the `warm_*` messages with `not_active`.
  - A warm `auth` whose key is on the chain host list may omit the password. Host links and probes authenticate this way (warm, no password). Client warm sessions still need the password.
  - Add a test case `TestHostWarmAuthNoPassword`: with `Password: "pw"`, a host key with warm auth and no password succeeds. A non-host key with warm auth and no password gets `auth_failed`.
- [ ] **Step 4: Run** `go test -race ./server`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(server): active/standby modes, warm sessions, 500 ms heartbeats`.

### Task 4: Host link, server side (the active host serving links)

**Files:**
- Create: `server/linkserve.go`
- Modify: `server/channels.go` (route `link_hello`, `link_probe`)
- Modify: `server/chain.go` (broadcast appended entries to links)
- Modify: `server/channels.go` `chat()` (forward to links)
- Test: `server/linkserve_test.go`

**Interfaces:**
- Consumes: Task 2 `History.After`, Task 3 modes.
- Produces:
  - `conn.link *linkState`. It is non-nil once a host sends `link_hello`, and only when `chain.State().IsHost(conn pubkey)`. Otherwise the reply is `forbidden`.
  - Link conns are removed from `authed` (no presence or roster) and are not counted by `reserveClient`.
  - On `link_hello`:
    - Send `chain` frames for `Since(ChainSeq)`. If `ChainSeq` is ahead of our head, reply `bad_chain`.
    - Send `link_sync{PasswordKey}` (Task 1b; `LinkSync.Password` becomes `PasswordKey []byte "password_key,omitempty"`).
    - Send `link_sync{Messages: After(HistoryLastTS, HistoryLastID, n), More}` pages until done.
    - Then stream live: each appended chain entry as a `chain` frame, each accepted chat as `link_sync{Messages:[m]}`, and `hb` every 500 ms.
  - `(s *Server) Links() []LinkPeer`, where `LinkPeer{Fingerprint, Name string; Addrs []string; Priority int; LastBeat time.Time}`.
  - `(s *Server) PickStandby() *LinkPeer`: the highest-priority linked host (lowest number, from `chain.State().Hosts`) that at least half of the authed clients report as reachable through `reach{fingerprint, ok}` (decision 5b). It returns nil when none qualifies. A newly linked host counts as reachable until the first reports arrive.
  - Test `TestStandbySkipsUnreachable`: B (priority 2) is reported unreachable by 2 of 3 clients, and C (priority 3) is reachable. The standby is C.
  - On any link change, the active host recomputes the standby:
    - It sends `link_role{Standby}` to each link.
    - It builds `proto.Hosts{Active: self, Standby: pick}` and broadcasts `hosts` to authed conns when it changed.
    - It sends `hosts` to every new authed conn after `auth_ok`.
  - `link_probe` (allowed on any authenticated conn whose key is a host) answers `link_probe_reply` with `Active`, `VoiceMembers`, `Clients`, our priority and `Self`. Non-host keys get `forbidden`.
  - A `host_remove` applied for a linked host closes its link.
  - The server needs its own `Self HostInfo`. Add `Config.HostName string` and `Config.Advertise []string`.

- [ ] **Step 1: Write the failing tests.** Use a chain with two hosts: build it with `newChain(keyA, cfg)` plus a founder-signed `host_add{keyB, 2}`. Helper: `twoHostChain(t) (c *chain.Chain, a, b *identity.Identity)`.
  - `TestLinkHelloStreamsChainHistoryPassword`: the active host has password "pw" and 3 chat lines. B dials with its key, authenticates and sends `link_hello{ChainSeq: 0}`. B receives every chain entry, then `link_sync` with "pw", then the 3 messages, then a live chat line and an `hb` within 1 s.
  - `TestLinkRefusesNonHost`: a key not on the host list sends `link_hello` and gets `forbidden`.
  - `TestLinkRefusesOlderChain`: a `link_hello` with `ChainSeq` beyond our head gets `bad_chain`.
  - `TestStandbyPickAndHostsBroadcast`: a full client is connected. Hosts B (priority 2) and C (priority 3) link. The client gets `hosts` with `Standby.Fingerprint == B`. B gets `link_role{Standby:true}` and C gets `false`. When B closes, the client gets `hosts` with standby C.
  - `TestLinkProbe`: the reply shows `Active:true`, `VoiceMembers:0`, `Clients:1`.
  - `TestHostRemoveDropsLink`: the founder appends `host_remove{B}`, and B's link closes.
- [ ] **Step 2: Run** `go test ./server -run 'Link|StandbyPick'`. Expected: FAIL.
- [ ] **Step 3: Implement `server/linkserve.go`.** History pages reuse the 60 KiB packing used by `encodeHistory`. All link sends go through `enqueue`, so a slow link is dropped like a slow client.
- [ ] **Step 4: Run** `go test -race ./server`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(server): serve host links (chain, history, password, live stream, standby pick)`.

### Task 5: Host link, dialing side (the non-active host mirroring)

**Files:**
- Create: `server/linkdial.go`
- Test: `server/linkdial_test.go`

**Interfaces:**
- Consumes: Task 4 wire behavior, and the `client` package handshake. Reuse it by exporting `client.Handshake(ctx, ws, id, name, password, pin string, warm bool) (*HandshakeResult, error)` from `client/client.go`, factored out of `handshake`. `HandshakeResult` has `Chain *chain.Chain` and `ServerID string`. `DialPinned` keeps working through it.
- Produces:
  - `type Link struct`.
  - `DialLink(ctx context.Context, url string, s *Server) (*Link, error)`:
    - Authenticates with `s.cfg.HostKey`, `warm=true`, password `""` (Task 3: host keys may warm-auth without a password). The `link_hello` that follows turns the warm conn into a link conn, but only on an active host; a non-active host answers `not_active`.
    - `ProbeHost(ctx, url string, s *Server) (proto.LinkProbeReply, error)` uses the same warm, password-less auth, sends `link_probe`, reads the reply and closes. Task 8 uses it.
    - Checks that the remote chain's ID equals ours.
    - Sends `link_hello` from our chain head and `History.Last()`.
  - `(l *Link) Run()` applies frames to the local server:
    - A chain entry goes to `s.chain.Append`, then `refreshGroup`, `applyHistoryLimits`, and a broadcast to our warm conns. A ban kicks the member.
    - `link_sync.Password` goes to `s.setPassword` (a new method guarded by `s.mu`).
    - `link_sync.Messages` goes to `s.hist.Merge`, and bumps `lastChatTS`.
    - `link_role` sets `l.Standby()`.
    - `hb` updates `l.LastBeat()`.
  - `(l *Link) ActiveAlive(now time.Time) bool`: true when `now - LastBeat < heartbeatGrace`.
  - `(l *Link) Done() <-chan struct{}` and `(l *Link) Close()`.
  - A local append that fails with `ErrConflict` or `ErrBadChain` closes the link with that error. The orchestrator handles it.

- [ ] **Step 1: Write the failing tests.** Two `Server`s in one process share the Task 4 `twoHostChain`; B runs with `Mode: ModeStandby`.
  - `TestLinkMirrorsChatAndChain`: a chat on A shows up in B's `hist.Page` within 1 s. A founder `grant` on A appears in B's `chain.Head()`.
  - `TestLinkReplicatesBan`: ban member M on A. B's chain state shows M banned. M's warm conn on B is kicked.
  - `TestLinkPasswordSync`: A has password "pw". After linking, a full client authenticating on B after promotion needs "pw".
  - `TestLinkActiveAlive`: alive right after linking. After `A.Shutdown`, `ActiveAlive(now+heartbeatGrace)` is false, and `Done()` closes.
- [ ] **Step 2: Run** `go test ./server -run 'LinkMirror|LinkRepl|LinkPass|LinkActive'`. Expected: FAIL.
- [ ] **Step 3: Implement.** First add `client.Handshake` and keep `go test ./client` green. Check for an import cycle: `client` must not import `server`. `server` importing `client` is fine if `client` has no server import. If a cycle appears, move the handshake into a new package `core/hshake` and use it from both.
- [ ] **Step 4: Run** `go test -race ./server ./client`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(server): dial and mirror the active host over the host link`.

### Task 6: Pre-auth chain rate limit and roster refresh on grant (PR 1 open items)

**Files:**
- Modify: `server/guards.go`
- Modify: `server/conn.go` `authenticate`
- Modify: `server/chain.go` `handleChainAppend`
- Test: `server/guards_test.go`, `server/chain_test.go`

**Interfaces:**
- Produces:
  - Per-IP chain send budget: `chainSendBudget = 2 << 20` bytes per minute per IP. When it is exceeded, the pre-auth connection is closed before the chain is sent. Uses `Server.clock`.
  - A `grant` or `revoke` append rebroadcasts `roster` to authed conns.
  - `ErrorMsg.Ref` is echoed for errors caused by a `chain_append` that carried `Ref`.

- [ ] **Step 1: Write the failing tests.**
  - `TestPreAuthChainBudget`: with a fake clock and a 400 KiB chain, the 6th handshake within a minute from the same IP is closed before any `chain` frame. After `Advance(time.Minute)` the next handshake succeeds.
  - `TestGrantRebroadcastsRoster`: the founder grants `admin` to M. Every authed client receives a `roster` where M's roles include `admin`.
  - `TestChainAppendErrorRef`: a stale-head append with `Ref:"r1"` gets `chain_conflict` with `Ref:"r1"`.
- [ ] **Step 2: Run.** Expected: FAIL.
- [ ] **Step 3: Implement.** In `client/chain.go`, `ChainAppend` sets a random `Ref` and matches errors by `Ref` instead of FIFO.
- [ ] **Step 4: Run** `go test -race ./server ./client`. Expected: PASS.
- [ ] **Step 5: Commit** `fix(server): pre-auth chain budget, roster on grant, error refs`.

### Task 7: Takeover and promotion

**Files:**
- Create: `server/takeover.go`
- Test: `server/takeover_test.go`

**Interfaces:**
- Consumes: Task 3 `promoteWarmLocked`, `SetMode`; Task 5 `Link.ActiveAlive`.
- Produces:
  - `(s *Server) SetLinkView(alive func(time.Time) bool, standby func() bool)`: set by the orchestrator so `takeover` can ask the link.
  - `takeover` on a warm conn behaves as follows:
    - If the mode is not standby, or `standby()` is false, it gets `not_standby`.
    - If `alive(now)`, it gets `active_alive`.
    - Otherwise the host promotes once, guarded by a `sync.Once`-style flag that resets on demotion:
      - `SetMode(ModeActive)` and `promoteWarmLocked()`.
      - Call the orchestrator hook `onPromote()`, set via `SetPromoteHook(func())`.
      - Add the system chat line `host changed to <cfg.HostName>` to every text channel's history and broadcast it with `ChatMessage.System=true`.
    - Every takeover that arrives during or after the promotion gets a full session, and the client sees `roster` and `hosts`.
  - A host no longer in `State().Hosts` refuses `takeover` with `host_not_authorized`.

- [ ] **Step 1: Write the failing tests.** A is active and B is standby, linked (Task 5). Shorten `heartbeatInterval` to 50 ms and `heartbeatGrace` to 150 ms.
  - `TestTakeoverActiveAlive`: a warm client on B sends `takeover` while A is up. It gets `active_alive`, and B stays standby.
  - `TestTakeoverPromotes`: run `A.Shutdown`, wait out the grace period, then `takeover`. B's mode is active, the client gets `roster` and `hosts{Active: B}`, and the history of `general` ends with a System line `host changed to b`.
  - `TestTakeoverConcurrent`: 5 warm clients send `takeover` at the same moment after A dies. The promote hook runs exactly once, and all 5 get a roster with 5 members.
  - `TestRemovedHostCannotPromote`: B has been removed through `host_remove` (while linked, so B has the entry). `takeover` gets `host_not_authorized`.
- [ ] **Step 2: Run** `go test ./server -run Takeover`. Expected: FAIL.
- [ ] **Step 3: Implement** `server/takeover.go`.
- [ ] **Step 4: Run** `go test -race ./server`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(server): takeover with active_alive check and one-shot promotion`.

### Task 8: Host orchestrator: cold start, standby, re-link, split-brain, handback

**Files:**
- Create: `server/host.go`
- Test: `server/host_test.go`

**Interfaces:**
- Consumes: Tasks 3 to 7.
- Produces:
  - `type HostConfig struct{ Server Config; Known func() []string; SaveKnown func([]string); Notify func(string) }`. `Known` returns known host URLs except our own advertised ones.
  - `NewHost(hc HostConfig) *Host`.
  - `(h *Host) Start() error`: starts the server in `ModeIdle`, then runs the loop.
  - `(h *Host) Shutdown(ctx) error`.
  - `(h *Host) Server() *Server`.
  - `(h *Host) Role() string`, one of `"active"`, `"standby"` or `"idle"`.
  - `(h *Host) Active() proto.HostInfo`.
  - Package vars: `coldStartWindow = 3*time.Second`, `probeEvery = 30*time.Second`, `handbackIdle = 2*time.Minute`.
- Loop rules (spec 5.1, 5.5, 5.6):
  1. **Cold start.** For up to `coldStartWindow`, `link_probe` each known URL.
     - The first reply with `Active:true` and the same server ID gets `DialLink`, and the host becomes standby or idle according to `link_role`.
     - If nothing answers, the host becomes active with `SetMode(ModeActive)`.
  2. **While linked.** The link's `Done()` fires when the active host is gone. Do nothing for `heartbeatGrace` to let client takeovers arrive. If still not active, cold start again: probe the known hosts plus the active host's advertised standby, using the last `hosts` seen on the link.
     - A promoted standby lets the other hosts find it through probes.
     - If no client sent `takeover` (for example because no clients exist), the standby itself becomes active after the cold start finds nobody.
  3. **While active.** Every `probeEvery`, `link_probe` the known hosts.
     - When another host answers `Active:true`, apply the yield rule with `(voiceMembers, clients, priority)`. Fewer voice members yields. On a tie, fewer clients yields. On a tie, the larger priority number yields.
     - **The loser:**
       1. Sends its history to the winner over a fresh `DialLink` (the winner gets the loser's `After(zero)` pages through a new `link_sync` direction: the dialing side may send `link_sync{Messages}` and the server merges them with `Merge`).
       2. Sends every authed client `move{To: winner, Reason: "merge"}`.
       3. Finds every chain entry it has that the winner's chain lacks (compare by `Hash` after the common prefix). For each connected author, it sends error `chain_lost` with the copy `your change was lost during a network split; please redo it`.
       4. Replaces its local chain with the winner's (`chain.FromEntries`, then `SetPath`).
       5. Sets `SetMode(ModeIdle)` and keeps the new link.
  4. **Handback.** While active, if a linked host has higher priority than us and voice has been empty for `handbackIdle`:
     - Send `handover` over that link. The receiver promotes like a takeover but without the `active_alive` check, and sends its `hosts`.
     - Send `move{To: that host, Reason: "handback"}` to clients.
     - Set `ModeIdle` and link to it.
  5. `SaveKnown` is called with the union of every `HostInfo.Addrs` seen (`hosts` on links and probe replies), capped at 16 URLs.
- On mode changes, `Notify` gets short lines for the UI log and notice: `hosting: active`, `hosting: standby for <name>`, `hosting: idle`.

- [ ] **Step 1: Write the failing tests.** Set `coldStartWindow=300ms`, `probeEvery=300ms`, `handbackIdle=300ms` and fast heartbeats. Three hosts share one chain (helper `threeHostChain`).
  - `TestColdStartAlone`: a single host becomes active within 500 ms.
  - `TestColdStartJoinsActive`: A is active. B starts with `Known=[A]` and becomes standby, and A's `Links()` contains B.
  - `TestStandbyBecomesActiveWithoutClients`: A is active and B is standby. After `A.Shutdown`, B is active within `heartbeatGrace + coldStartWindow + 200ms`.
  - `TestSplitBrainMerge`: A and B both start with no known hosts, so both become active. Each gets one chat line, and B gets one founder `grant` appended (B is the founder's host here). Then set `Known` on both. Within 1 s exactly one is active. The winner is the one with more clients: give A one client. Both lines are in the winner's history. B's grant author, connected to B, gets `chain_lost`. B's chain head equals A's.
  - `TestHandbackWhenVoiceEmpty`: B (priority 2) is active and has a client. A (priority 1) starts and links as standby. After `handbackIdle`, A is active, the client got `move{Reason:"handback"}`, and B is standby or idle.
  - `TestNoHandbackDuringVoice`: same as the previous test, but a client is in voice with synthetic membership (a `voiceSet` entry through the in-package helper). After 3×`handbackIdle`, B is still active.
- [ ] **Step 2: Run** `go test ./server -run 'ColdStart|StandbyBecomes|SplitBrain|Handback'`. Expected: FAIL.
- [ ] **Step 3: Implement `server/host.go`.** One goroutine owns the state machine, with a `select` over link `Done`, timers and a stop channel. All mode changes happen on that goroutine.
- [ ] **Step 4: Run** `go test -race ./server -count=3 -run 'ColdStart|StandbyBecomes|SplitBrain|Handback'`. Expected: PASS three times (flake check).
- [ ] **Step 5: Commit** `feat(server): host orchestrator (cold start, failover, split-brain, handback)`.

### Task 9: Warm WebRTC peer on the standby and adoption on voice join

**Files:**
- Create: `server/warmpeer.go`
- Modify: `server/sfu.go` (`sessionJoinLocked` / peer creation adopts a warm PC)
- Test: `server/warmpeer_test.go`

**Interfaces:**
- Produces:
  - `warm_offer`, `warm_answer` and `warm_candidate` are allowed on warm conns in any mode.
  - The server creates a PeerConnection with `newRTCAPI` (its mux and settings) and accepts one data channel labelled `"hermec-warm"`. It answers, and keeps `s.warmPeers[conn] = pc`.
  - A data-channel message `"ping"` is answered with `"pong"`, as a keepalive.
  - After promotion, when that conn sends `voice_join`, the voice session's `newPC(p)` takes `s.warmPeers[conn]` instead of creating a new PeerConnection, then registers the usual track handlers. The client's next `rtc_offer` renegotiates the same PC.
  - `warmPeers` entries close on conn close and on demotion.

- [ ] **Step 1: Write the failing tests** (pion client side inside the test, loopback ICE).
  - `TestWarmPeerDataChannel`: a warm conn offers a data channel. The answer arrives, the DC opens, and `ping` gets `pong` within 3 s.
  - `TestWarmPeerAdoptedOnVoiceJoin`: after promotion, `voice_join` plus an `rtc_offer` adding an Opus track on the same client PC lands in the voice session. Assert that the server's `peer.pc` is the same pointer as `warmPeers[conn]`, and that RTP from the client reaches a second voice member.
- [ ] **Step 2: Run** `go test ./server -run WarmPeer`. Expected: FAIL.
- [ ] **Step 3: Implement.** The pion `OnDataChannel` handler is the only data-channel code on the server.
- [ ] **Step 4: Run** `go test -race ./server -run 'WarmPeer|Voice'`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(server): warm data-channel peer on the standby, adopted by voice join`.

### Task 10: Client: hosts, heartbeat watchdog, warm session, takeover, rollback pin

**Files:**
- Create: `client/failover.go`
- Modify: `client/client.go` (`Event` fields, `readLoop` cases, `DialPinned` options)
- Test: `client/failover_test.go`

**Interfaces:**
- Produces:
  - `type DialOpts struct{ Pin string; MinChainSeq uint64; Warm bool }`.
  - `DialWith(ctx, url string, id *identity.Identity, name, password string, o DialOpts) (*Client, error)`. `DialPinned` calls it.
  - A chain whose head `Seq` is below `MinChainSeq` fails with `bad_chain` and the message `host served an older chain`.
  - `Event` gains:
    - `Hosts *proto.Hosts`
    - `Move *proto.Move`
    - `ActiveLost bool`: emitted once when no `hb` arrives for `heartbeatGrace=1500ms`, measured only after the first `hb` (v0.6 hosts send none, so no false alarms).
  - `(c *Client) Takeover(ctx) error` on a warm client:
    - It returns `nil` after promotion. The client is now a full session, and the read loop starts emitting normal events.
    - It returns `*ServerError{Code: CodeActiveAlive}` or `CodeNotStandby` otherwise.
  - `(c *Client) Warm() bool`.
  - `(c *Client) ChainSeq() uint64`.
  - Clock seam: `watchdog(now func() time.Time)`, plus the package var `heartbeatGrace`.

- [ ] **Step 1: Write the failing tests** (against real `server.Server`s from the server package, as the existing client tests do).
  - `TestActiveLostAfterSilence`: with a fast heartbeat, after `Shutdown` the client gets `Event{ActiveLost:true}` within 300 ms.
  - `TestNoActiveLostOnOldHost`: a stub host that never sends `hb` produces no `ActiveLost` within 2 s.
  - `TestWarmTakeoverPromotes`: A is active and B is standby, linked. A warm client on B: after A dies, `Takeover` returns nil, and `Join("general")` works.
  - `TestTakeoverActiveAliveErr`: while A is alive, the error code is `active_alive`.
  - `TestRollbackRefused`: `MinChainSeq: head+1` gives `bad_chain`.
  - `TestHostsEvent`: a full client gets `Event{Hosts}` after auth.
- [ ] **Step 2: Run** `go test ./client -run 'ActiveLost|Warm|Takeover|Rollback|HostsEvent'`. Expected: FAIL.
- [ ] **Step 3: Implement** `client/failover.go`.
- [ ] **Step 4: Run** `go test -race ./client`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(client): warm sessions, heartbeat watchdog, takeover, rollback pin`.

### Task 11: Client voice: warm peer and call transfer

**Files:**
- Modify: `client/voice.go` (`call`)
- Create: `client/warmpeer.go`
- Test: `client/warmpeer_test.go`

**Interfaces:**
- Consumes: Task 9 server behavior, Task 10 warm client.
- Produces:
  - `(c *Client) OpenWarmPeer(ctx) error` on a warm client: creates a PC with one data channel `"hermec-warm"`, sends `warm_offer`, applies `warm_answer`, and pings every 5 s.
  - `(c *Client) WarmPeerReady() bool`.
  - `(c *Client) AdoptCall(ctx, from *Client) error`:
    - Moves the active `call` (channel, mute state, source, sink, video wants) from the dead client `from` to `c`. `c` must be a promoted (formerly warm) client.
    - Sends `voice_join(channel)`.
    - Adds the Opus track to the warm PC and runs the normal `startOffer` on that PC.
    - Applies `voice_mute` if muted.
    - Returns once `checkReady` fires.
    - With no warm PC, it falls back to `JoinVoice` with the same source and sink.

- [ ] **Step 1: Write the failing tests.** A is active and B is standby. Use `markerSource`/`recSink` patterns copied from `integration/voice_test.go` into a client test helper.
  - `TestWarmPeerOpens`: `WarmPeerReady()` within 3 s.
  - `TestAdoptCallResumesAudio`: clients X and Y are in voice on A, each with a warm client and warm peer on B. Kill A, then `Takeover` and `AdoptCall` on both. Y's sink receives X's marker again within 1.5 s of the takeover. The gap between X's last frame from A and its first from B is under 1500 ms.
  - `TestAdoptCallKeepsMute`: X muted on A is still muted on B (`voice_state` shows muted).
- [ ] **Step 2: Run** `go test ./client -run 'WarmPeer|AdoptCall'`. Expected: FAIL.
- [ ] **Step 3: Implement.** The `call` actor gets a `rebind(cl *Client, pc *webrtc.PeerConnection)` path. Keep `reset()` and glare behavior unchanged for the new PC.
- [ ] **Step 4: Run** `go test -race ./client`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(client): warm voice peer and in-place call transfer on failover`.

### Task 12: UI sessions: known hosts, warm session, failover, move, rollback pin, "hosted by"

**Files:**
- Modify: `ui/state/servers.go` (`ServerEntry`)
- Modify: `ui/session.go`
- Modify: `ui/voicepanel.go`
- Modify: `ui/state/state.go` (`HostedBy string`)
- Test: `ui/session_test.go`, `ui/state/servers_test.go`

**Interfaces:**
- Consumes: Tasks 10 and 11.
- Produces:
  - `ServerEntry` gains:
    - `Hosts []string "toml:\"hosts,omitempty\""`: known host URLs, max 16.
    - `ChainSeq uint64 "toml:\"chain_seq,omitempty\""`: the highest chain seq seen.
    - Helpers: `SetHosts(entries, url, hosts)` and `BumpChainSeq(entries, url, seq)`.
  - `session` gains `warm *client.Client`, `standby proto.HostInfo` and `hostedBy string`.
  - Flow:
    - On `Event.Hosts`: save `Active.Addrs` plus `Standby.Addrs` into `Hosts`, and set `st.HostedBy = Active.Name`. If the standby changed, close `s.warm` and dial the standby's first reachable address with `DialOpts{Warm:true, Pin, MinChainSeq}`. If in a call, `OpenWarmPeer`.
    - On `Event.ActiveLost`, or on events closing while `s.warm` is set: `Takeover`.
      - Success: swap `s.c = s.warm`, `s.warm = nil`. If in a call, run `AdoptCall(old)`. Rejoin the text channel being viewed. No reconnect screen, and the tile never shows failed.
      - `active_alive`: close the warm session and redial the active host's URL immediately (attempt 0).
      - Any other error: the normal reconnect loop.
    - On `Event.Move`: dial `Move.To.Addrs` as a new full client. On success swap clients, carrying the call over with `JoinVoice` and the same mute state. Close the old client.
    - Reconnect loop: when the saved URL fails, try each `Hosts` URL in order before backing off. A `not_active` error makes the next dial go to the active host from the preceding `hosts` event without delay.
    - On `Event.Chain` and after dialing: `BumpChainSeq`. Dial with `MinChainSeq = entry.ChainSeq`.
  - Voice panel: adds `hosted by <name>` as part of `voicePanelLine()` (`<channel> / <server> · hosted by <name>`) when `HostedBy` is set and differs from the server name. Clip it to the panel width with the existing clipping helper.

- [ ] **Step 1: Write the failing tests** (`newTestGame`, two in-process `server.Host`s).
  - `TestSessionFailoverSwapsClient`: A is active and B is standby, with the game connected to A. Within 1 s it has `s.warm != nil`. After A is shut down, `pumpUntil` shows `s.c` connected to B with `s.failed==false`, and the viewed channel is joined.
  - `TestSessionActiveAliveRedials`: drop only the game's connection to A (close `s.c`'s socket from the test). The session reconnects to A. B stays standby.
  - `TestSessionMoveFollows`: a `move` event dials the target, and the old client closes.
  - `TestServersHostsAndChainSeqPersist`: SaveServers then LoadServers round-trips `Hosts` and `ChainSeq`.
  - `TestHostedByLine`: `voicePanelLine()` contains `hosted by b` after failover.
- [ ] **Step 2: Run** `go test ./ui -run 'Failover|ActiveAlive|MoveFollows|HostedBy' ./ui/state -run Hosts`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./ui ./ui/state`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(ui): known hosts, warm standby session, seamless failover and move`.

### Task 13: UI hosting: the founder's server and friend hosts through the orchestrator

**Files:**
- Modify: `ui/hosted.go` (founder hosting uses `server.Host`)
- Create: `ui/hosting.go` (friend hosts)
- Modify: `ui/menu.go` (tile menu toggle)
- Test: `ui/hosting_test.go`

**Interfaces:**
- Consumes: Task 8 `NewHost` / `HostConfig`.
- Produces:
  - `hostedServer.srv` becomes `*server.Host`. Callers use `.Server()` where they need the server. `startHosted` and `stopHosted` call `Host.Start` and `Host.Shutdown`. `Known` and `SaveKnown` read and write `hosted.toml` under a new `known_hosts` key in `MarshalLocalTOML`.
  - `type friendHost struct{ h *server.Host; id, path string; port int }`, with `g.friends map[string]*friendHost` keyed by server ID.
  - Files: `<config>/hermec/hosting/<server-id>.toml` with keys `port`, `password`, `known_hosts`, `enabled`.
  - `(g *game) syncFriendHosting(s *session)`: runs on connect and on every chain event.
    - When `s.c.Chain().State().IsHost(g.id.Public())`, we are not the founder of an app-hosted server, and `enabled` is not false: persist the chain to `chains/<id>.jsonl` (`SetPath`) and start a `friendHost`. The port is the first free one from 7697 to 7707, and history goes to `history/<id>.jsonl`.
    - When the host is removed or the toggle is off: `Shutdown` and keep the files.
    - At app start, `bootFriends()` starts every `enabled` hosting file whose chain still lists us.
  - Tile menu: for a server where we are a host, the item `let my PC host this server: on` or `…: off` toggles `enabled` and starts or stops the host. Switching it on for the first time shows the notice `friends must be able to reach this PC (share it on Tailscale)`.
  - Task 12 sessions send `reach{fingerprint, ok}` after each warm dial attempt to the standby. `reach` is added to Task 1's types: `TypeReach="reach"`, `Reach{Fingerprint string; OK bool}`.
  - The advertised addresses are `g.selfAddrs()` combined with the port.

- [ ] **Step 1: Write the failing tests.**
  - `TestFriendHostStartsWhenGranted`: the founder server A is in-process. The game connects as member G. A appends `host_add{G, 2}`. Within 1 s `g.friends[id]` exists, its role is `standby`, and `hosting/<id>.toml` exists with `enabled = true`.
  - `TestFriendHostToggleOff`: activating the toggle menu item stops the host and writes `enabled = false`. After a restart (`bootFriends`) it stays off.
  - `TestFriendHostStopsOnRemove`: `host_remove{G}` stops the host.
  - `TestHostedUsesOrchestrator`: `createHosted` then `g.hosted.srv.Role()=="active"`.
- [ ] **Step 2: Run** `go test ./ui -run 'FriendHost|HostedUsesOrch'`. Expected: FAIL.
- [ ] **Step 3: Implement.** The `hosting/` dir is created at 0700, and files are written 0600 like `hosted.toml`.
- [ ] **Step 4: Run** `go test -race ./ui`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(ui): friend hosting with the per-server toggle; hosted server runs the orchestrator`.

### Task 14: UI roster host controls (founder)

**Files:**
- Modify: `ui/members.go` (row hit rects)
- Modify: `ui/state/hits.go` (`HitMember`)
- Modify: `ui/menu.go` (`openMemberMenu`)
- Modify: the input handling that currently opens the tile menu, extended to member rows
- Test: `ui/members_test.go`

**Interfaces:**
- Produces:
  - `HitMember` hits carry the row's fingerprint.
  - Right-clicking a member row when `s.c.Fingerprint() == chain founder` opens a menu:
    - `Allow hosting`: shown when the member is not a host. Sends `ChainAppend(host_add{PubKey: member.PubKey, Priority: max+1})`.
    - `Stop hosting`: shown when the member is a host other than the founder. Sends `host_remove{fp}`.
    - `Move up`: shown when the member is a host with priority > 2. It swaps priority with the host above: two `host_add` appends, the lower one first. It is never shown for the founder (priority 1 stays the founder's).
  - Non-founders get no member menu.
  - `ChainAppend` errors show through the existing notice path.

- [ ] **Step 1: Write the failing tests.**
  - `TestMemberMenuFounderItems`: as founder, right-click the row of non-host M. The items are exactly `[Allow hosting]`. Activating it gives a chain head of `host_add` with M's key and priority 2.
  - `TestMemberMenuMoveUp`: hosts are F(1), B(2), C(3). The menu on C shows `Stop hosting` and `Move up`. After Move up, the priorities are B=3 and C=2.
  - `TestMemberMenuNonFounderNone`: as an admin who is not the founder, right-click opens nothing.
- [ ] **Step 2: Run** `go test ./ui -run MemberMenu`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./ui`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(ui): founder roster menu: Allow hosting, Stop hosting, Move up`.

### Task 15: hermec-server host flags and the end-to-end integration suite

**Files:**
- Modify: `cmd/hermec-server/main.go`
- Create: `integration/failover_test.go`
- Test: `cmd/hermec-server/main_test.go`

**Interfaces:**
- Produces:
  - `-advertise` takes a comma list of ws URLs and defaults to `ws://<non-loopback IPs>:<port>/`.
  - `-hosts` takes a comma list of known host URLs. They are merged with the `known_hosts` saved in a `<config dir>/known_hosts.txt` file, one URL per line.
  - `-name` sets the host display name and defaults to the hostname.
  - `main` runs `server.NewHost` instead of `server.New`.
  - A single host with no `-hosts` behaves as today: active after the 3 s cold start. The startup log says `hosting: active`.
  - Integration helper: `startHosts(t, n int) []*server.Host`, which builds n hosts on one chain (founder key = host 0) with loopback ICE and fast timers, each knowing the others.

- [ ] **Step 1: Write the failing tests** in `integration/failover_test.go`.
  - `TestFailoverWithin2s`: 3 hosts. Two voice clients use the UI-equivalent client flow (warm session, warm peer, `Takeover`, `AdoptCall`; helper `followFailover(t, c)`), and both chat. Kill the active host. Within 2 s both clients are full sessions on the standby. Voice markers flow again, and a chat line sent after the failover reaches the other client. The history of `general` has the System line.
  - `TestClientBlipNoFailover`: client X connects through `newWSProxy`. Cutting the proxy for 2 s gives X `active_alive` and a reconnect to the same active host. The standby is still standby.
  - `TestStandbyReplacedThenFailover`: kill the standby first. Clients get `hosts` with the third host as standby and re-warm. Then kill the active host, and failover reaches the third host.
  - `TestSplitBrainChatMerge` and `TestHandbackOnlyWhenVoiceEmpty`: end-to-end versions of the Task 8 tests through real clients.
  - In `cmd/hermec-server/main_test.go`, `TestFlagsAdvertiseHosts`: parse `-hosts a,b -advertise c` and assert the resulting HostConfig.
- [ ] **Step 2: Run** `go test ./integration -run 'Failover|Blip|Standby|SplitBrain|Handback' ./cmd/hermec-server`. Expected: FAIL.
- [ ] **Step 3: Implement** the flags and helpers. Fix whatever the end-to-end tests expose; each fix goes to the owning task's file with its own unit test.
- [ ] **Step 4: Run** `go test -race ./... -count=1`. Then run `go test ./integration -run Failover -count=5` (flake check). Expected: PASS.
- [ ] **Step 5: Commit** `feat: hermec-server host flags; failover integration suite`.

### Task 16: Docs

**Files:**
- Modify: `README.md` ("How people reach each other", "How safe is this?", hosting)
- Modify: `docs/protocol.md`
- Modify: `docs/quickstart-friends.md`
- Modify: `HANDOFF.md`
- Modify: `example.server.toml` / hermec-server usage text

- [ ] **Step 1: README updates.**
  - Explain how hosting works now: active, standby, failover in about a second, handback when voice is empty, "let my PC host this server", and Allow hosting.
  - Add to "How safe is this?" the spec 4.3 line, verbatim in substance: `the active host and its standby can read and keep chat for up to history_days; set it to 0 to keep nothing.`
  - Also say that every host knows the server password.
- [ ] **Step 2: `docs/protocol.md`.** Add a host link section covering `link_hello`, `link_sync`, `link_role`, `link_probe`, `handover` and heartbeats. Add warm sessions (`auth.warm`, `takeover`, `warm_*`), `hosts`, `move`, the new error codes, `Member.pub_key`, and `ref`. Rewrite in place.
- [ ] **Step 3: HANDOFF.md.**
  - Remove the "For PR 3" items this plan closed: rollback, pre-auth budget, refs, and the roster on grant.
  - Add Victor's real-test checklist: a friend call where Victor quits HERMEC mid-call with a friend's machine as standby; a handback after the call; Allow hosting and Move up from the roster.
  - Add new deferred items found during the round.
- [ ] **Step 4: Run** `go vet ./... && gofmt -l .` (empty) and `go test ./...`. Expected: PASS.
- [ ] **Step 5: Commit** `docs: hosting, failover and host link`.

At round end, after Victor's real test, delete the spec and this plan (Victor's docs rule) and tag v0.7.0 on `main`.
