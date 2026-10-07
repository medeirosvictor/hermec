# Hermec Server Discovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The connect screen lists live Hermec servers it can find — on the LAN (UDP broadcast) and on a Tailscale tailnet (peer enumeration + unicast probe) — so joining a friend's server is a click, not a typed `ws://` string.

**Architecture:** A tiny discovery protocol: servers (opt-out) answer a UDP probe on their listen port with a small JSON identity (name, port, version). The client probes three ways — LAN broadcast, unicast to tailnet peers (enumerated via `tailscale status --json` when the CLI exists), and unicast to saved-server hosts — and merges results into a "Discovered" list on the connect scene. Pure merge/sort/probe-independent logic lives in testable packages; one new client-side package `discover`, one server-side responder. No new dependencies.

**Tech Stack:** stdlib net (UDP), os/exec for the optional tailscale CLI query. Nothing else.

**Spec:** docs/specs/2026-10-06-hermec-design.md §1 (easily accessible, portable), §5 (standards: the probe format gets documented); Hamachi-level-2 decision in PLAN.md roadmap item 4 (confirmed 2026-10-07).

## Global Constraints

- No new dependencies. The tailscale integration is EXEC-ONLY (`tailscale status --json`), fully optional: absence of the CLI = silently no tailnet results.
- Discovery protocol v1 (document in docs/protocol.md as its own short section): client sends UDP datagram `HERMEC_DISCOVER_1` (exact ASCII bytes) to port 7697 (or the server's configured port); server replies from the same socket with one JSON datagram `{"hermec":1,"name":"<server_name>","port":<ws port>,"ver":"<version string>"}` ≤512 bytes. Anything else is ignored by both sides. No amplification: reply is sent ONLY to the probing source addr, one reply per probe, rate-limited to 10 replies/sec per source.
- Server config: `discoverable = true` (default) and `server_name` (default: host:port) — a host can hide (`discoverable = false`).
- Client probe budget: total discovery pass ≤1.5s (broadcast wait 1s; unicast probes concurrent, 64 max in flight, 750ms timeout each); never blocks the Ebiten loop (house pattern: goroutine + results channel drained in Update).
- Discovered entries are session-only (never written to servers.toml until the user actually connects — then the existing Touch/Save path applies).
- gofmt/vet clean; TDD for everything below the GUI; full `go test -count=1 ./...` + `go test -race -count=1 ./server/... ./discover/...` before each commit (PATH needs /c/msys64/ucrt64/bin, CGO_ENABLED=1 for -race). Commit trailer: `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`.

## Review Focus

1. The UDP responder must not be an amplification vector: reply only to the probe's source, reply ≤512B (smaller than… comparable to the probe? probe is 17B — document the honest ratio), per-source rate limit, and `discoverable=false` means the socket isn't even opened — Task 1 tests.
2. A malformed/oversized/garbage UDP datagram (either direction) must be ignored without logging storms or panics — fuzz-style table tests in Task 1 (server) and Task 2 (client parse).
3. Discovery must degrade silently: no tailscale CLI, no broadcast permission, firewalled UDP — the connect screen just shows no/fewer results, never an error dialog, never a hang — Task 2 tests + Task 3 manual checks.
4. A discovered server the user clicks may be gone — the normal dial-failure path (ConnectErr, stay usable) must handle it; click-to-connect reuses the existing flow — Task 3 manual check.
5. The probe must not fire on every frame — only on connect-scene entry, manual refresh (R), and a ≥5s periodic tick while the scene is visible — Task 3 (wiring reviewed, not just eyeballed).

---

### Task 1: Discovery protocol + server responder

**Files:**
- Create: `discover/proto.go` (shared packet format), `server/discovery.go`
- Modify: `server/config.go` (+`discoverable`, `server_name`), `server/server.go` (start/stop responder with the server), `example.server.toml`, `docs/protocol.md` (new "Discovery" section)
- Test: `discover/proto_test.go`, `server/discovery_test.go`

**Interfaces:**
- `discover` package (shared, importable by client and server):
  - `const Magic = "HERMEC_DISCOVER_1"`
  - `type Announce struct { Hermec int `json:"hermec"`; Name string `json:"name"`; Port int `json:"port"`; Ver string `json:"ver"` }`
  - `func EncodeAnnounce(a Announce) ([]byte, error)` (errors if >512B), `func ParseAnnounce(b []byte) (Announce, error)` (strict: hermec==1, port 1-65535, name ≤64 runes — else error)
- Server: responder goroutine bound to the SAME port as the ws listener but UDP; lifecycle tied to Start/Shutdown (joined on shutdown); per-source token bucket 10/s (reuse the Task-7 bucket type); `discoverable=false` → no UDP socket at all.
- `Version`: a package-level `var Version = "dev"` in a new `version` package (both binaries report it; the release plan will stamp it via ldflags — put it in `version/version.go` now so that plan has its hook).

- [ ] **Step 1: Failing tests** — proto round-trip + reject table (garbage, oversized name, hermec:2, port 0); responder over loopback UDP: probe → one valid Announce from the server's addr; garbage probe → silence; `discoverable=false` → dial refused/no listener; 20 rapid probes from one source → ≤10+burst replies (injected clock if the bucket supports it).
- [ ] **Step 2: RED → implement → GREEN; -race on ./server/... ./discover/...**
- [ ] **Step 3: protocol.md Discovery section (packet bytes, reply schema, rate limit, opt-out) + example.server.toml keys**
- [ ] **Step 4: Commit** — `feat: discovery protocol and server responder`

### Task 2: Client-side probing

**Files:**
- Create: `discover/probe.go`, `discover/tailscale.go`
- Test: `discover/probe_test.go`, `discover/tailscale_test.go`

**Interfaces:**
- `type Found struct { Announce; Addr string /* host:port for ws dial */; Source string /* "lan"|"tailnet"|"saved" */ }`
- `func Probe(ctx context.Context, opts ProbeOpts) []Found` — fires all three probe kinds concurrently, dedupes by Addr (priority lan>tailnet>saved for Source labeling), returns within opts budget (default 1.5s). `ProbeOpts{ Port int; ExtraHosts []string /* saved-server hosts */; TailscalePeers func() []string /* seam; nil = use CLI */ }`
- LAN: one UDP socket, send Magic to 255.255.255.255:port (+ each interface's broadcast addr), collect replies until deadline.
- Tailnet: `tailscalePeers()` runs `tailscale status --json` (LookPath first; absent → nil, no error), parses peer Tailscale IPs (online only), returns them; Probe unicasts Magic to each (64 concurrent cap).
- Saved: unicast to ExtraHosts (so saved-but-sleeping servers show live/dead status later — v1 just includes live ones in the list).
- All parse failures ignored per-packet; the function NEVER returns an error (empty slice is the failure mode).

- [ ] **Step 1: Failing tests** — probe against a real responder on loopback (from Task 1) finds it; dedupe logic (same server via lan+saved → one entry, Source "lan"); tailscale JSON parsing from a fixture (real `tailscale status --json` shape — peers online/offline filtering); CLI-absent path returns nil peers without error; ctx cancellation returns early.
- [ ] **Step 2: RED → implement → GREEN; -race**
- [ ] **Step 3: Commit** — `feat: LAN, tailnet, and saved-host discovery probing`

### Task 3: Connect-scene discovered list

**Files:**
- Modify: `ui/connect.go`, `ui/app.go`; pure list/selection logic in `ui/state` (+tests)
- Test: ui/state additions TDD; Ebiten layer build + manual checklist

**Interfaces:**
- Probe runs in a goroutine on: connect-scene entry, R keypress, and a 5s ticker while the scene is active (stop the ticker when the scene isn't); results land via channel into Update (house pattern); at most one probe in flight (`probing` flag).
- Connect scene renders a "DISCOVERED" section under the fields: up to 6 rows — `name  (source)  host:port`, bright on hover/selection; click = fill URL field AND immediately start the dial (one click = joining); keyboard: Up/Down selects within the list, Enter connects to selection (Tab still switches fields).
- Dedupe against the rail's saved servers (a discovered server that's already saved shows its saved label).
- Empty result = section hidden entirely (no "nothing found" noise on first run).
- `-local` runs: the embedded server is discoverable too (it exercises the whole path in one process — and the manual checklist uses it).

- [ ] **Step 1: TDD the pure parts (list merge with saved, selection model) in ui/state; implement scene + wiring**
- [ ] **Step 2: Build/vet/full suite + -race ./ui/...**
- [ ] **Step 3: Manual checklist:** `-local` instance discovers itself; second machine/VM on the LAN sees it (or two instances on one machine — loopback broadcast reaches both? verify and note); with Tailscale running, a tailnet server appears within ~2s; R refreshes; killing the server drops it on next refresh; click joins in one action; no stutter while probing; `discoverable = false` hides the server.
- [ ] **Step 4: Commit** — `feat: discovered-servers list on the connect screen`

### Task 4: Docs

**Files:** Modify: `docs/quickstart-friends.md`, `README.md`

- [ ] **Step 1: Quickstart: simplify the LAN and Tailscale paths — friends now SEE the server in "Discovered" instead of typing addresses (keep the typed-address fallback for the public-internet path); README one-liner + `discoverable`/`server_name` keys; verify no stale claims.**
- [ ] **Step 2: Full suite green; commit** — `docs: discovery in the friends quickstart`
