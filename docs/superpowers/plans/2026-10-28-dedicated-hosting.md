# Dedicated AWS/VPS Hosting Implementation Plan

**Status:** Tasks 1–5 are complete locally. Remote hosting is not available
until Task 6 passes and the work is merged and released.

## Goal and constraints

Make an operator-owned Linux VPS usable without project-operated infrastructure
or cloud credentials. Public signaling uses trusted `wss://`; media UDP must be
reachable separately. Preserve server ID, signed chain and host authorization;
never copy founder or member private keys to a VPS. Admission (password proof or
allowlist), explicit persistent state/logs/backups/upgrades, and same-build
peers are required.

## Completed evidence (Tasks 1–4)

- Bootstrap/validation, service-ready Linux bundles and lifecycle materials.
- Provider-neutral and AWS operator guidance plus `hermec-verify` for pinned
  WSS signaling and two-endpoint media proof.
- Desktop Dedicated Add flow: WSS-only address, pinned server ID, password
  admission, temporary JoinVoice UDP/ICE check before save, proof-key-only
  persistence, and versioned share/import details.
- Commits: Tasks 1–2 `ca6ed69`, Task 3 `020492f`, Task 4 `5df0524`.

## Task 5: Authorize and prepare a VPS host — complete locally

- Founder authorizes the VPS-generated public key as a host and transfers
  chain/history through the authenticated host-link model while the VPS remains
  prepared, not independently active.
- Preparation proof only establishes Ready; it never fences or cuts over.
  The current chain founder explicitly confirms the exact destination
  fingerprint and transition nonce before the source performs the durable
  fenced cutover.
- Never transfer member private keys. The cutover revalidates destination
  authorization, server ID, chain head, history boundary, nonce, source mode,
  and voice emptiness under one gate. Structured pending/ready/retry/error
  results give fixed recovery guidance without changing authority.

## Task 6: Acceptance and release

### Automated evidence — 2026-10-28 (Windows amd64)

**Scope and isolation.** This run did not provision or contact a cloud resource,
run a GUI, or use an operator's application configuration. Before tests,
`HOME`, `APPDATA`, and `LOCALAPPDATA` were set to the ignored, stable
`dist/task6-automated/{home,appdata,localappdata}` directories; release output
was kept under `dist/task6-automated/build/release`. `CGO_ENABLED=1` and
`PATH=/c/msys64/ucrt64/bin:$PATH` selected the MSYS UCRT GCC for the race run.

**Network/media audit.** Static review of every `*_test.go` network constructor
and test command confirmed execution is limited to `127.0.0.1`, `::1`, or
`httptest`; the client and server `TestLoopbackOnlyFilter` tests enforce the
ICE restriction. URL strings for `example.test`, GitHub, and private addresses
are parser/validation fixtures, not dial targets. The dedicated-focused suite
therefore used only loopback listeners and in-process test TLS certificates.
No test command was pointed at a VPS, AWS, LAN address, or a real app config.

All commands below exited zero. The full command output is reproducible in the
ignored `dist/task6-automated/logs/` directory for this run.

```sh
# Formatting (canonicalize this Windows checkout's CRLF only before comparing).
git ls-files '*.go' | while IFS= read -r f; do
  cmp -s <(tr -d '\r' < "$f") <(tr -d '\r' < "$f" | gofmt) || printf '%s\n' "$f"
done
# PASS (no unformatted tracked Go files)

git diff --check                         # PASS (no output)
go vet ./...                              # PASS (no output)
go test -count=1 ./...                    # PASS: all packages

CGO_ENABLED=1 PATH=/c/msys64/ucrt64/bin:$PATH \
  go test -race -count=1 ./client ./server ./cmd/hermec-server \
  ./cmd/hermec-verify ./core/chain ./core/identity ./core/names \
  ./core/proto ./core/roles ./discover ./integration ./netpath ./update ./version
# PASS (netpath and version have no test files)

go test -count=1 ./cmd/hermec-server \
  -run '^(TestInitDedicated|TestCheckDedicatedState|TestUpdateAndRollback|TestUpdateRejects|TestUpdateRollsBack|TestValidatePublicConfig|TestPublicTLS|TestPublicConfigRejects|TestDedicatedHost|TestDedicatedPrepare)'
go test -count=1 ./server \
  -run '^(TestLoadConfig|TestLoadExampleFile|TestServerTLS|TestServerTLSMismatchedConfig|TestDedicatedActivate|TestDedicatedTransition|TestPreparingTransition|TestValidateDedicatedPrepared|TestStartRetry|TestRestorePreparing|TestTerminalTransition|TestPreparedHost|TestDedicatedProof|TestInstallDedicatedBootstrap|TestPreparedLink|TestFencedServer|TestDedicatedVoice|TestDedicatedReadiness|TestDedicatedCommit|TestSourceReadiness|TestReset)'
go test -count=1 ./client ./core/proto \
  -run '^(TestParseDedicatedHostPublicKey|TestCanonicalDedicatedWSS|TestDedicated)'
# PASS: bootstrap/config/service rollback; TLS/admission; protocol/client and
# transition rollback/admission suites
```

Release-compatible pure-Go builds used `CGO_ENABLED=0`,
`TAG=task6-automated`, `-trimpath`, and the release workflow's version
ldflags. The exact build commands (with `ROOT` set to the repository root from
the isolated release directory) were:

```sh
ldflags="-s -w -X github.com/medeirosvictor/hermec/version.Version=$TAG"
for goarch in amd64 arm64; do
  GOOS=linux GOARCH=$goarch go build -trimpath -ldflags "$ldflags" \
    -o "stage-server-$goarch/hermec-server" "$ROOT/cmd/hermec-server"
  GOOS=linux GOARCH=$goarch go build -trimpath -ldflags "$ldflags" \
    -o "stage-server-$goarch/hermec-verify" "$ROOT/cmd/hermec-verify"
  # Copy the same lifecycle files as .github/workflows/release.yml, then:
  tar -czf "dist/hermec-server-linux-$goarch.tar.gz" -C "stage-server-$goarch" .
done
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$ldflags" \
  -o dist/hermec-server-windows-amd64.exe "$ROOT/cmd/hermec-server"
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$ldflags" \
  -o dist/hermec-verify-windows-amd64.exe "$ROOT/cmd/hermec-verify"
test "$(dist/hermec-server-windows-amd64.exe -version)" = "hermec-server $TAG"
test "$(dist/hermec-verify-windows-amd64.exe -version)" = "hermec-verify $TAG"
dist/hermec-verify-windows-amd64.exe -mode init -identity verifier.identity
tar -tzf dist/hermec-server-linux-amd64.tar.gz
tar -tzf dist/hermec-server-linux-arm64.tar.gz
sha256sum dist/*; wc -c dist/*
```

Linux server and verifier were built and packaged for amd64 and arm64; the
workflow-supported Windows amd64 server was built, and the CI-supported Windows
amd64 verifier was also built. Native Windows server/verifier `-version` checks
and verifier `-mode init` passed. Both Linux archives passed `tar -tzf` and
each contains the server, verifier, example config, service unit, lifecycle
scripts, and README.

| artifact | bytes | SHA-256 |
| --- | ---: | --- |
| `hermec-server-linux-amd64.tar.gz` | 10,097,959 | `e392da7f8be95b64720c7ebdf0bc4f2d94add9d4dfb454ec40a11393e5a30882` |
| `hermec-server-linux-arm64.tar.gz` | 9,037,575 | `7adb83a2208ee946c01d5e67bf8537c4551eeaf261f6bf2027965564c6cbb960` |
| `hermec-server-windows-amd64.exe` | 13,438,464 | `62830cab039e5766d15f0e1f00d5f5ca91fbd6fccbb6f05a6c091037601d2f66` |
| `hermec-verify-windows-amd64.exe` | 11,978,240 | `2910cc0e2bd07fdf7d1fc2cc8d38755b8a50b20c5d584ccd6a05cd6b88d18325` |

**Not passed / deliberately not attempted.** This is only the automated gate.
The disposable-VPS and AWS gates below remain unrun and are not approved by
this evidence.

- Automated gate: root vet/race suite, bootstrap/config/service, TLS/admission
  and transition rollback tests, release cross-builds, and `git diff --check`.
- Disposable-VPS gate: fresh install; DNS/TLS; join; two-way text/audio;
  camera/selected screen; restart persistence; backup/restore; update/rollback;
  blocked-UDP diagnosis; and host failover/recovery.
- AWS gate: security group permits required TCP and UDP, a real remote
  friend/client connects, and no project infrastructure is used.
- Only after these gates pass may docs describe availability and a minor release
  be published.
