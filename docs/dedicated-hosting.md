# Dedicated server hosting

HERMEC can run as an operator-owned Linux systemd service. HERMEC does not
create cloud resources, receive cloud credentials, or manage billing.

A public server requires:

- a DNS name in `public_dns` and a trusted, currently-valid TLS certificate
  covering that name for `wss://` signaling;
- inbound TCP 7697 and UDP 7697 (or the configured ports);
- password or member-key admission;
- persistent storage for `server.toml`, `hermec-server.key`, `chain.jsonl`, and
  `history.jsonl`.

The first release expects Linux amd64 or arm64 and systemd. A small group can
start with 1-2 vCPU and 1 GiB RAM. Camera and screen traffic can create material
egress charges; check the provider's transfer prices and alerts.

## Install

1. Use a Linux amd64 or arm64 system with systemd and persistent storage for
   the state directory. For public use, arrange DNS, a trusted certificate, and
   firewall access to the configured TCP and UDP ports; TCP alone allows
   signaling but **not calls**.
2. Download the dedicated bundle matching the machine, verify it against
   `SHA256SUMS.txt`, extract it, and run `sudo ./install.sh`.
3. Save the generated join password from the one-time bootstrap output in a
   password manager. The server stores only its derived proof key.
4. Obtain a trusted certificate (for example with Certbot or Caddy) and deploy
   a copy of its full chain and private key to:

       /var/lib/hermec/tls/fullchain.pem
       /var/lib/hermec/tls/privkey.pem

   Install them with ownership and modes the service can read without exposing
   the private key (adjust the source paths for your certificate provider):

       sudo install -o hermec -g hermec -m 0600 /path/to/fullchain.pem /var/lib/hermec/tls/fullchain.pem
       sudo install -o hermec -g hermec -m 0600 /path/to/privkey.pem /var/lib/hermec/tls/privkey.pem

   Arrange renewal deployment with the same ownership/modes and restart the
   service after renewal.
5. Set `public_dns` in `/var/lib/hermec/server.toml` to the DNS name on the
   certificate. If the host uses 1:1 NAT, also set `public_ip` to its stable
   public IP. Do not change `public = true`, `discoverable = false`, or the
   generated `password_key`.
6. Validate before binding sockets:

       sudo -u hermec /usr/local/bin/hermec-server \
         -config /var/lib/hermec/server.toml -check

7. Enable the service:

       sudo systemctl enable --now hermec-server
       sudo systemctl status hermec-server
       sudo journalctl -u hermec-server -f

Share `wss://your-name.example:7697/`, the server ID printed by `-check`, and the
join password through separate trusted channels where practical.

## Backup and restore

Create a consistent backup (the script briefly stops and restarts an active
service):

    sudo ./backup.sh /safe/location/hermec-YYYYMMDD.tar.gz

The archive contains the host private identity, so protect it like a password
and never publish it. Test restores on a disposable machine.

To restore:

1. Stop HERMEC and move the current `/var/lib/hermec` aside; do not merge two
   identities or chain files.
2. Create a fresh mode-0700 `/var/lib/hermec`, extract the archive there, and
   set ownership to `hermec:hermec`.
3. Run `hermec-server -config /var/lib/hermec/server.toml -check`.
4. Confirm the server ID equals the backed-up server ID, then start the service.

A restore deliberately preserves the server ID, chain, history, admission key,
and TLS material.

## Update and rollback

Download and verify a new dedicated bundle, then run:

    sudo ./update.sh update ./hermec-server

The script first verifies that the downloaded candidate reports a HERMEC
version and runs its `-config /var/lib/hermec/server.toml -check` validation
while an active service is still running. It then retains the previous binary,
installs the new one atomically, waits for systemd to report it active, and
rolls back automatically if it fails to become active.
Persistent state is not modified. Manual rollback is:

    sudo ./update.sh rollback

Back up first and keep the previous bundle until calls, restart persistence,
and logs have been checked.

## Troubleshooting

- **Text connects but calls fail:** verify inbound and outbound UDP for the
  configured media port, `public_ip`, the guest firewall, and NAT mapping.
- **TLS check fails:** set `public_dns` to the certificate DNS name; use the
  full certificate chain from a system-trusted issuer, ensure it is unexpired,
  and let the `hermec` service user read the private key.
- **Server ID changed:** stop immediately and restore the original
  `hermec-server.key` and `chain.jsonl` together. Do not ask members to accept an
  unexpected identity.
- **Service restart loop:** inspect `journalctl -u hermec-server`; run `-check`
  directly. Raw diagnostics stay in the operator's journal and should be
  scrubbed before sharing.
