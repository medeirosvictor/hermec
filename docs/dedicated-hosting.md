# Dedicated server hosting

> **Unreleased development documentation.** Dedicated hosting is not available
> in current releases. Do not use this guide for public onboarding until Task 6
> passes and the work is merged and released.

HERMEC can run as an operator-owned Linux systemd service. It does not create
cloud resources, receive cloud credentials, or manage billing. The dedicated
Linux amd64/arm64 archive contains both `hermec-server` and `hermec-verify`.
Use the same released build for server and verifier: pre-v1 protocol
compatibility between arbitrary builds is not promised.

For a beginner-oriented, click-by-click AWS EC2 disposable-test setup, see
[From no AWS account to a disposable HERMEC VPS](aws-ec2-from-zero.md). It does
not replace this guide's verifier, founder cutover, backup, or recovery details.

## Provider-neutral VPS preparation

Use a Linux amd64 or arm64 VM with systemd, a stable public IPv4/IPv6 address,
and persistent block storage. A small voice/chat group can start with 1--2
vCPU, 1 GiB RAM, and a modest persistent disk; size disk and network capacity
for history, backups, cameras, and screen sharing. Attach/retain the disk
before installing, and back it up independently. Video and screen sharing can
produce substantial provider egress charges: check transfer pricing and set
budget/usage alerts.

Create a DNS record for the stable address and obtain a trusted, currently
valid certificate for that exact name. Permit the configured signaling TCP port
and the media UDP port in **both** the provider firewall and the host
firewall. By default `media_port = 0` uses the signaling listener's port for
media (7697 only when `addr` uses its default); a nonzero `media_port` requires
that UDP port instead. Also permit the required outbound UDP replies and normal
HTTPS/DNS/certificate-renewal egress. TCP-only rules allow signaling but do not
make calls work. Do not expose SSH broadly; use administrator IP restrictions
or a bastion.

Keep `/var/lib/hermec` on persistent storage: it contains `server.toml`,
`hermec-server.key`, `chain.jsonl`, `history.jsonl`, TLS material, and admission
configuration. Use `systemctl status hermec-server` and
`journalctl -u hermec-server -f` for service logs. Back up before upgrades, use
the supplied update script, and retain the previous archive for rollback.

### AWS EC2 example

Choose an EC2 Linux instance matching the downloaded architecture. For a small
initial amd64 deployment, `t3.small` is a concrete starting point; use
`t3.medium` or a general-purpose `m6i` size when sustained CPU or more memory
is needed. For arm64, use a Graviton-compatible Linux image and start with
`t4g.small`; `t4g.medium` or an `m7g` size is the corresponding step up. These
are instance-family/size examples, not price or capacity promises: measure the
workload and review current AWS limits and pricing.

Attach an EBS volume for `/var/lib/hermec`, allocate/associate an Elastic IP if
the address must not change, and create a Route 53 (or other DNS) record for
it. Its security group must allow TCP 7697 and UDP 7697 from the intended users
(and constrained SSH from administrators); mirror those rules in `ufw`,
nftables, or the distro firewall. Configure EBS snapshots/backups,
CloudWatch/billing alarms, and certificate renewal. EC2 data transfer and
cross-AZ traffic can cost money; review the current AWS pricing rather than
assuming a free allowance.

## Install

1. Download the matching dedicated bundle, verify it against `SHA256SUMS.txt`,
   extract it, and run `sudo ./install.sh`.
2. Save the one-time bootstrap join password in a password manager. The server
   stores only its derived proof key.
3. Deploy trusted certificate files (owned/readable only by the service):

       sudo install -o hermec -g hermec -m 0600 fullchain.pem /var/lib/hermec/tls/fullchain.pem
       sudo install -o hermec -g hermec -m 0600 privkey.pem /var/lib/hermec/tls/privkey.pem

4. Set `public_dns` in `/var/lib/hermec/server.toml` to the certificate DNS
   name. Set `public_ip` for stable 1:1 NAT. Do not change generated
   `password_key`.
5. Validate then enable:

       sudo -u hermec /usr/local/bin/hermec-server -config /var/lib/hermec/server.toml -check
       sudo systemctl enable --now hermec-server

Share `wss://name.example:7697/`, the server ID printed by `-check`, and the
join password through separate trusted channels.

## Moving an existing group to this VPS

This is a two-party, founder-confirmed cutover; preparing a VPS does **not**
activate it. Stop the new VPS service while preparing it. On the VPS, print
only its public host identity:

    sudo -u hermec /usr/local/bin/hermec-server -config /var/lib/hermec/server.toml -dedicated-host-key

A current founder must add that public key as a host using the existing active
server's normal authenticated host-management flow. Never copy a founder,
member, or old-host private key to the VPS. Then, on the VPS, use the active
host's trusted public TLS URL and its already-known server ID:

    sudo -u hermec /usr/local/bin/hermec-server -config /var/lib/hermec/server.toml \
      -dedicated-prepare wss://active.example:7697/ -dedicated-server-id SERVER_ID

The command authenticates using only the VPS host key, pins the URL's server
ID, mirrors and attests the exact chain/history boundary, records `preparing`
before waiting for the source ACK, then records `prepared`, and exits. It
prints no password or private key. A crash in either state remains passive and
may retry only the saved URL, boundary, and nonce. Start the service afterwards:

    sudo systemctl start hermec-server
    sudo -u hermec /usr/local/bin/hermec-server -config /var/lib/hermec/server.toml -dedicated-status

A prepared service remains passive and resumes its saved URL and exact nonce;
it cannot elect itself or activate. Have the founder explicitly inspect and
confirm the prepared destination/fingerprint and nonce in the client before
requesting cutover. Only that founder action can fence the old host and commit
the destination.

### Founder desktop sequence

On the connected active-server tile, the current chain founder can choose
**Manage dedicated host…**. This is not the end-user **Dedicated server** Join
page. Paste only the VPS public key printed by `-dedicated-host-key`; the client
parses it and displays its fingerprint. Confirm **Authorize** explicitly. The
client adds it at one lower-precedence priority after every current host (the
largest existing priority plus one), then asks the active source for Ready for
that exact fingerprint.

Ready is not activation. Compare the displayed VPS fingerprint and exact
transition nonce with the prepared VPS, then press **Activate** explicitly.
The source will retry rather than cut over while anyone is in voice. A timeout
or lost response is an unknown outcome: leave the page open, use **Check
Ready**, and retry only after the exact Ready boundary/nonce is displayed
again. Closing the page or losing the session clears the draft; no VPS key or
nonce is saved by the desktop client, and it never activates automatically.

If the prepare command was interrupted after it created `transition.json`, do
not run prepare again and do not delete the record. Inspect it, then retry its
saved URL and exact boundary:

    sudo -u hermec /usr/local/bin/hermec-server -config /var/lib/hermec/server.toml -dedicated-status
    sudo -u hermec /usr/local/bin/hermec-server -config /var/lib/hermec/server.toml -dedicated-retry

`-dedicated-retry` accepts only a validated `preparing` or `prepared` record,
never generates a nonce, changes neither the pinned source URL nor the exact
boundary, and never activates a host. `-dedicated-status` validates and reports
`preparing`, `prepared`, `fenced`, or `committed`, the destination fingerprint,
and the public transition nonce for exact client comparison; a malformed or unrelated
record is an error requiring operator investigation.

If the active source's readiness attestation becomes obsolete because its
chain, history, or destination authorization changed, leave it active and
inspect it with `-dedicated-attestation-status`. The authenticated, authorized
local source operator may run `-dedicated-attestation-reset` to abort the obsolete
attempt; it refuses prepared/fenced sources and any state with a cutover
certificate or fence. It clears the old nonce before another preparation, so
an old destination cannot replay it. Do not remove journal files manually.

## Remote reachability verifier

`hermec-verify` is a pure-Go, two-endpoint proof of authenticated pinned WSS
signaling and bidirectional synthetic audio through the SFU. It is not a local
bind test or signaling-only test. It never accepts a password in an argument or
environment variable: pipe it once on stdin. It uses system TLS trust and has
no insecure TLS option.

On each independently administered remote machine, create a distinct verifier
identity locally--never copy a member or founder private key to a VPS. The
initializer creates the file exclusively at mode 0600 and prints its public
fingerprint; it refuses to replace an existing file:

    ./hermec-verify -mode init -identity /secure/a.identity

Exchange only those printed fingerprints through a trusted channel. Put the
other endpoint's fingerprint in a local mode-0600 text file (one
`abcd-efgh-ijkl-mnop` fingerprint and optional whitespace). Both identities
must be admitted by the server and both operators must use the same voice
channel. Then run simultaneously, changing identity and peer file on the other
machine:

    < /secure/join-password ./hermec-verify \
      -url wss://name.example:7697/ -server-id SERVER_ID \
      -identity /secure/a.identity -expected-peer /secure/b.fingerprint \
      -channel verify -timeout 90s -frames 10 -diagnostics /secure/a-report.json

Do not put the password literally in shell history, an argument, or an
environment variable; use a password-manager pipe, protected file descriptor,
or a mode-0600 password file redirected to stdin. The verifier accepts at most
256 password bytes (excluding a final line ending); longer terminated or
unterminated input is rejected. The optional report is allowlisted JSON only,
atomically written mode 0600, and contains stage/code, elapsed time,
counts, server ID, and fingerprints--never password, keys, SDP, ICE candidates,
or server transport errors. It is operator evidence, not an independently
attested certificate of reachability; retain and interpret it with the endpoint
and server logs.

A success requires the expected peer to appear in voice state and enough valid
Opus comfort-noise frames carrying that peer's authenticated marker and
monotonic sequence. The verifier treats the channel as exclusive: any third
participant fails the proof rather than treating its media as evidence. Since
both endpoints send those decodable synthetic audio frames, two successful,
separately run invocations provide reciprocal operator evidence of both
directions. Failures are deliberately coarse and diagnostic rather than a
root-cause verdict: `URL_TLS` covers URL, DNS, certificate, or system trust;
`PIN_IDENTITY` covers pin, identity, or fingerprint file; `ADMISSION` covers
password/admission (including input timeout); `PEER_NOT_PRESENT` means the
expected endpoint was not observed in the channel; `NEGOTIATION_UDP` means voice
negotiation did not complete and may involve UDP/firewall/NAT/media; and
`AUDIO_INVALID` means unexpected sender or insufficient/invalid marker audio.

## Disposable-VPS acceptance checklist

Before declaring public reachability, use a disposable trusted public server
and **two independently administered remote VPSes/networks**. Pin the checked
server ID, exchange distinct identity fingerprints, run the bundled verifier on
both endpoints, and retain only its safe diagnostics. Confirm each report says
success and records the expected peer frames. CI, loopback/local tests, and a
successful server `-check` do **not** establish internet reachability.

## Backup, update, and troubleshooting

Create a consistent backup with `sudo ./backup.sh /safe/location/hermec.tar.gz`.
The script stops an active service while it snapshots the following private
state: `server.toml`, `hermec-server.key`, `chain.jsonl`, `history.jsonl`, and,
when they exist, `transition.json` and `readiness.json`. The latter two are the
durable dedicated cutover/readiness records; retain them exactly, including for
`prepared`, `fenced`, or `committed` transitions. A server that never took
part in a cutover has neither file.

The archive is mode 0600 and includes private identity, so protect it like a
password. It deliberately excludes `logs/` and `tls/`, including TLS private
keys. Keep TLS material in its certificate-management backup or redeploy it
from that source; do not add it through `HERMEC_BACKUP_EXTRA` unless an
operator has a separate documented key-handling policy.

To restore, stop the service, extract the archive into a mode-0700 matching
state directory owned by `hermec`, restore/deploy its TLS files separately,
and preserve the extracted mode-0600 file permissions. Then validate before
starting:

    sudo systemctl stop hermec-server
    sudo install -d -o hermec -g hermec -m 0700 /var/lib/hermec
    sudo tar -xzf /safe/location/hermec.tar.gz -C /var/lib/hermec
    sudo chown -R hermec:hermec /var/lib/hermec
    sudo -u hermec /usr/local/bin/hermec-server -config /var/lib/hermec/server.toml -check
    sudo systemctl start hermec-server

Confirm the expected server ID from `-check` before starting. If the server ID
changes, stop and restore the original key and chain together.

Update with `sudo ./update.sh update ./hermec-server`; it validates the
candidate while the service runs and rolls back on failure. Use
`sudo ./update.sh rollback` for manual rollback. If text works but calls fail,
check inbound/outbound UDP, guest/provider firewalls, NAT and `public_ip`. If
the server ID changes, stop and restore the original key and chain together.
