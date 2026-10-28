# Dedicated server hosting

> **Unreleased development documentation.** Dedicated hosting is not available
> in current releases. Do not use this guide for public onboarding until Task 6
> passes and the work is merged and released.

HERMEC can run as an operator-owned Linux systemd service. It does not create
cloud resources, receive cloud credentials, or manage billing. The dedicated
Linux amd64/arm64 archive contains both `hermec-server` and `hermec-verify`.
Use the same released build for server and verifier: pre-v1 protocol
compatibility between arbitrary builds is not promised.

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
and the media UDP port(s) in **both** the provider firewall and the host
firewall. By default `media_port = 0` uses the signaling listener's port for
media (7697 only when `addr` uses its default); a nonzero `media_port` requires
that UDP port instead, while `udp_port_min`/`udp_port_max` require the entire
configured UDP range. Also permit the required outbound UDP replies and normal
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
The archive includes private identity and must be protected like a password.
Restore only a complete matching state directory, validate with `-check`, and
confirm its server ID before starting.

Update with `sudo ./update.sh update ./hermec-server`; it validates the
candidate while the service runs and rolls back on failure. Use
`sudo ./update.sh rollback` for manual rollback. If text works but calls fail,
check inbound/outbound UDP, guest/provider firewalls, NAT and `public_ip`. If
the server ID changes, stop and restore the original key and chain together.
