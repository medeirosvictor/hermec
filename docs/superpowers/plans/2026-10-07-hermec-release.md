# Hermec Release & Update Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Tagged releases built by CI (a Windows client zip with its DLLs, Linux/Windows server binaries), version-stamped binaries, and an in-app update banner — so Victor's friends install once and get told when to update, instead of being mailed exes. Ends by tagging **v0.1.0**, Hermec's first real release. Plus: the GUI idle-CPU fix (the footprint report's known hot spot).

**Architecture:** GitHub Releases is the distribution home (no new infrastructure). A release workflow fires on `v*` tags: a Windows job (MSYS2 toolchain) builds the client and bundles the opus DLLs into one zip; a Linux job cross-builds the pure-Go servers. `version.Version` is stamped via ldflags. A tiny `update` package checks the GitHub releases API once at startup (opt-out in settings, silent on failure, never for "dev" builds) and the GUI shows an amber banner when a newer tag exists.

**Spec:** PLAN.md roadmap item 5 (Victor's banner-before-distribution requirement, 2026-10-07, design confirmed in conversation); docs/footprint.md (the idle-CPU target).

## Global Constraints

- No new Go dependencies. The update check uses stdlib net/http + encoding/json against `https://api.github.com/repos/medeirosvictor/hermec/releases/latest`; 3s timeout; ALL failures silent (offline/blocked GitHub must never degrade the app). `version.Version == "dev"` → no check at all.
- Privacy: the check sends no identifying data beyond the TLS connection itself; `update_check = true` default in settings.toml with a settings-scene toggle; README documents the check and the opt-out in one honest sentence.
- Semver comparison: tags are `vMAJOR.MINOR.PATCH`; compare numerically, not lexically; a malformed remote tag → silent no-banner.
- Release workflow uses only: actions/checkout, actions/setup-go, msys2/setup-msys2, and the preinstalled `gh` CLI for release creation/upload. Artifacts: `hermec-windows-amd64.zip` (hermec.exe + required DLLs + a one-page README.txt), `hermec-server-linux-amd64`, `hermec-server-linux-arm64`, `hermec-server-windows-amd64.exe`, `SHA256SUMS.txt`. All builds `-ldflags "-s -w -X github.com/medeirosvictor/hermec/version.Version=${TAG}"`.
- The DLL set is determined EMPIRICALLY in CI (ntldd/objdump walk of hermec.exe against the msys2 ucrt64 bin dir), not hardcoded — a hardcoded list rots.
- GUI idle CPU: after Task 4, `hermec -local` sitting connected and idle must sample **<10% of one core** (footprint.md method); record before/after numbers in the task report.
- gofmt/vet clean; TDD below the Ebiten layer; full `go test -count=1 ./...` + `go test -race -count=1 ./update/... ./ui/...` per commit (PATH /c/msys64/ucrt64/bin, CGO_ENABLED=1). Commit trailer: `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`.

## Review Focus

1. The update check must be un-crashable and un-blockable: GitHub down, DNS dead, 404, garbage JSON, huge response (cap body read at 64KB), redirect loops — app runs identically — Task 3 table tests against a stub HTTP server.
2. Version comparison edge cases: v0.10.0 > v0.9.9 (numeric), equal versions → no banner, remote OLDER than local → no banner, "dev" local → no check — Task 3 tests.
3. The release zip must run on a clean Windows machine with NO MSYS2/Go installed — the DLL walk must catch transitive deps (libogg, winpthread, gcc_s). CI can't fully prove "clean machine"; Task 5's dry run + Victor's friend-machine test are the gates — say so in the workflow comments.
4. The banner must not lie: it appears only when remote > local, names the version, and the keypress opens the real release page URL from the API response (not a hardcoded URL pattern) — Task 4 wiring review + manual check.
5. The layout cache (idle-CPU fix) must be invisibly correct: same rendered output for cache hit and miss (new message, channel switch, resize, theme/font change via F2 all invalidate) — Task 4 state tests pin the invalidation key.

---

### Task 1: Version stamping + local release build script

**Files:**
- Create: `scripts/build-release.ps1`
- Modify: `version/version.go` (doc comment only, if needed)
- Test: manual verification steps below

**Interfaces — Produces:**
- `scripts/build-release.ps1 -Tag v0.0.0-local` builds all four binaries into `dist/` with Version stamped (verify via `dist/hermec-server-windows-amd64.exe` logging `hermec-server v0.0.0-local`), bundles the client zip INCLUDING the empirically-walked DLL set (ntldd from msys2, falling back to a documented objdump walk), and writes SHA256SUMS.txt. This script is the same logic the CI workflow will run — CI calls into it where the platform matches (Windows job), so local and CI builds can't drift.
- `dist/` added to .gitignore.

- [ ] **Step 1: Implement the script (build matrix, ldflags stamp, DLL walk, zip, checksums)**
- [ ] **Step 2: Run it locally; verify: version in startup logs of both exes; zip contains hermec.exe + DLLs + README.txt; `dist/` ignored by git**
- [ ] **Step 3: Kill-test the DLL claim as far as locally possible: run the zip's exe from a shell with a STRIPPED PATH (no msys2, no Go) — it must start (window opens) without DLL errors**
- [ ] **Step 4: Commit** — `feat: release build script with version stamping and DLL bundling`

### Task 2: Release workflow

**Files:**
- Create: `.github/workflows/release.yml`; Modify: `README.md` (one "Releases" line pointing at the GitHub releases page)

**Interfaces — Produces:**
- On push of tag `v*`: windows job (msys2/setup-msys2 with ucrt64 gcc/opus/opusfile/pkgconf + ntldd, setup-go, run scripts/build-release.ps1 for the client zip + windows server exe) and linux job (setup-go, CGO off, server amd64+arm64). A final job collects artifacts, generates SHA256SUMS.txt across all, and `gh release create "$TAG" --generate-notes` with all files attached.
- Workflow comments state the clean-machine caveat (Review Focus 3) and that the existing ci.yml still gates correctness (release.yml builds, never tests).

- [ ] **Step 1: Write the workflow (validate YAML by careful read; no local execution possible)**
- [ ] **Step 2: Commit** — `feat: tagged release workflow` (the real execution test is Task 5's dry run)

### Task 3: Update check (`update` package + settings)

**Files:**
- Create: `update/update.go`; Modify: `ui/state/settings.go` (+`UpdateCheck *bool`), `ui/settings.go` (toggle row)
- Test: `update/update_test.go`, settings test additions

**Interfaces — Produces:**
- `update.Check(ctx context.Context, current string) (Available, bool)` with `Available{Version, URL string}` — bool false when: current is "dev", request fails, malformed tag, remote <= current. Endpoint overridable via an unexported package var for tests (stub HTTP server).
- `func newer(remote, local string) bool` — exported-for-test or internal with table tests (Review Focus 2 cases).
- Settings: `update_check` (*bool, default true — same pattern as Scanlines); settings scene gains an "update check: on/off" row; flipping it persists immediately (toggle pattern).

- [ ] **Step 1: Failing tests** — newer() table (v0.10.0>v0.9.9, equal, older-remote, malformed, missing v prefix); Check against a stub server: happy path, 404, garbage JSON, >64KB body capped, timeout (short ctx), dev-skips-entirely (no request made — assert via stub hit counter); settings round-trip.
- [ ] **Step 2: RED → implement → GREEN; -race**
- [ ] **Step 3: Commit** — `feat: update check with settings opt-out`

### Task 4: Update banner + GUI idle-CPU fix (`ui`)

**Files:**
- Modify: `ui/app.go` (startup check goroutine + banner state + U key), `ui/main_scene.go`/`ui/connect.go` (banner line), `ui/state` (layout cache seam + tests)
- Test: ui/state cache-invalidation tests; manual checklist

**Interfaces — Produces:**
- Startup: if update_check enabled and Version != "dev", run update.Check in a goroutine (house pattern, result via channel); banner state `g.update *update.Available`.
- Banner: one amber line (bright) at the top of connect AND main scenes: `update vX.Y.Z available — press U to download`; `U` (with no text-input focus conflict: use Ctrl+U if plain U collides with chat typing — check how Ctrl+M/Ctrl+R are handled and match) opens Available.URL in the default browser (`exec.Command("rundll32", "url.dll,FileProtocolHandler", url)` on windows; build-tagged no-op logging the URL elsewhere). Banner dismissible with Esc? No — keep it one-line persistent (it's the product requirement); note in report if it fights the layout.
- **Idle-CPU fix:** cache the scrollback layout in mainScene keyed on (active channel, message count, last message timestamp, wrap cols, font size) — recompute only on key change (Review Focus 5 invalidation set: new message, channel switch, resize, F2 palette keeps size → no invalidation needed unless FontSize changes, theme file reload). Pure key/invalidation logic in ui/state with tests. ALSO check Rows()/per-frame allocations for cheap wins while in there (measure, don't guess).
- Measure before/after with the footprint method (15s CPU sample, connected idle `-local`); record both numbers in the report; target <10% of one core.

- [ ] **Step 1: TDD the cache key/invalidation in ui/state; implement banner + cache + wiring**
- [ ] **Step 2: gofmt/vet; full suite + -race ./ui/...**
- [ ] **Step 3: Measure idle CPU before/after (report numbers); brief -local smoke**
- [ ] **Step 4: Commit** — `feat: update banner; perf: cache scrollback layout`

### Task 5: Docs + v0.1.0 dry run

**Files:** Modify: `README.md`, `docs/quickstart-friends.md`; the tag + release are the deliverable

- [ ] **Step 1: Docs — README "Install" section leads with the Releases page (download zip, unzip, run — no toolchain needed); quickstart's "what friends receive" section now says "send them the Releases link" (keep the manual-zip path as a fallback note); the update-check privacy sentence + opt-out; CHANGELOG-lite: release notes come from --generate-notes.**
- [ ] **Step 2: Full suite green; commit docs** — `docs: install from releases, update check notes`
- [ ] **Step 3: DRY RUN: push tag v0.1.0; watch the workflow (gh run watch); download hermec-windows-amd64.zip from the release; run the exe from a clean-PATH shell; verify version in the log and the window opening. If the workflow fails: fix (new commits + move the tag or bump to v0.1.1), max 3 iterations before escalating to the controller.**
- [ ] **Step 4: Report the release URL — this is the "send it to your friends" green light.**
