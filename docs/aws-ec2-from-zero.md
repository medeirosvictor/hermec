# From no AWS account to a disposable HERMEC VPS

> **Unreleased dedicated hosting.** This is a test-runbook, not a statement that
> public dedicated hosting is available. Task 6 remote acceptance has **not**
> passed; do not use this for public onboarding or claim the remote gate passed.
>
> Never paste AWS access keys, SSH private keys, TLS private keys, join/bootstrap
> passwords, or founder/member private keys into HERMEC, chat, tickets, logs, or
> shell history. Keep secrets in a password manager or protected local files.

A **VPS** (virtual private server) is a rented virtual computer that runs all
the time in a provider data centre. Amazon **EC2** is AWS's service for renting
those virtual computers: an EC2 *instance* is the VPS in this guide. AWS bills
for the instance, disk, public IPv4 address, data transfer, backups, and other
resources; HERMEC does not create, pay for, or receive credentials for any of
them.

This first server is deliberately disposable: use it to collect Task 6 evidence,
then remove it. For verifier, founder cutover, backup, update, and recovery
procedures, use the [dedicated hosting guide](dedicated-hosting.md) rather than
trying to reproduce them here.

## 1. Secure the AWS account before launching anything

1. Create an AWS account at <https://aws.amazon.com/> using an email address you
   control. Complete identity/payment verification. Treat the account root user
   as the owner, not a daily administrator.
2. Sign in as root once, enable an MFA device in **IAM → Security credentials**,
   and store recovery details safely. Do **not** create root access keys.
3. In **IAM**, create an administrative human identity (prefer IAM Identity
   Center; an IAM user with AdministratorAccess is an acceptable small-test
   fallback), enable MFA for it, and use that identity for routine work. Sign
   out of root. Least-privilege roles can replace this admin later.
4. In **Billing and Cost Management**, enable billing alerts/cost visibility as
   needed, create a monthly AWS Budget with email alerts at a small threshold
   and at 100%, and confirm the alert email. Budget alerts are delayed and do
   not stop resources automatically: check the billing console too.
5. Pick one region near the test clients and keep EC2, its EBS volume, Elastic
   IP, and Route 53 record plan there. Instance availability, IPv4 pricing, and
   transfer pricing vary by region; review AWS's current prices before launch.

## 2. Launch the smallest useful EC2 test machine

In **EC2 → Instances → Launch instances** in the chosen region:

1. Name it `hermec-task6-disposable`. Select **Ubuntu Server 24.04 LTS,
   64-bit (x86)** (amd64) and instance type **t3.small**. This matches the
   Linux amd64 bundle and is a starting point, not a performance or price
   guarantee. For arm64, choose Ubuntu 24.04 LTS 64-bit (Arm), a Graviton
   type such as `t4g.small`, and the Linux arm64 bundle instead.
2. Create/download an EC2 SSH key pair. Save its private `.pem` file only on
   your administrator computer, restrict it (`chmod 600 key.pem` on a Unix
   shell), and never upload it to HERMEC or paste it anywhere. AWS cannot
   recover a lost private key; use EC2 Instance Connect/SSM only if you have
   deliberately configured them.
3. Under **Network settings**, create/select a VPC and a **public subnet**.
   A public subnet has a route to an Internet Gateway. Enable auto-assign public
   IPv4 or explicitly request a public IP at launch; without it, direct SSH and
   test-client reachability will not work. A default VPC is fine for this
   isolated disposable test; do not casually modify a shared production VPC.
4. Set storage to **20 GiB gp3 EBS**. EBS is persistent block storage (a virtual
   disk), but it is billable and not a backup. Keep its default encryption on.
5. Create a security group such as `hermec-task6-sg`. Inbound rules:

   | Protocol | Port | Source | Purpose |
   | --- | ---: | --- | --- |
   | TCP | 22 | **My IP** / your fixed admin CIDR | SSH only |
   | TCP | 7697 | CIDRs/IPs of intended test clients | HERMEC signaling |
   | UDP | 7697 | CIDRs/IPs of intended test clients | HERMEC voice/media |
   | TCP | 80 | temporary, `0.0.0.0/0` and `::/0` | Certbot HTTP-01 only |

   Restrict 7697 to the actual remote-test client networks whenever possible.
   Remove TCP 80 after the certificate is issued; it must be opened again only
   while a standalone renewal runs. The default outbound-allow-all rule is
   suitable for this test (DNS, package updates, HTTPS and UDP replies).
   TCP 7697 alone permits chat/signaling; calls also require UDP 7697.
6. Launch. Record the instance ID and current public IPv4 in a private operator
   note, not in chat. The automatically assigned public IPv4 changes on
   stop/start and AWS charges for public IPv4 addresses. For a stable DNS target,
   allocate and associate an **Elastic IP** in **EC2 → Elastic IP addresses**.
   An Elastic IP/public IPv4 has a cost warning: review current regional IPv4
   pricing, and release it when it is no longer needed. An allocated but
   unassociated Elastic IP can cost even more.

## 3. DNS, login, and base OS setup

Create an `A` record such as `hermec-test.example.com` pointing to the Elastic
IP. In Route 53 use the hosted zone's record creation screen; at another
registrar, create the same `A` record there. Wait until public DNS resolves:

```sh
nslookup hermec-test.example.com
```

From your administrator computer, connect using the DNS name (replace the
placeholders; do not put secrets in commands):

```sh
ssh -i /safe/path/hermec-task6.pem ubuntu@hermec-test.example.com
```

On the instance, update it, enable the uncomplicated firewall, and verify time
sync. Keep the existing SSH session open while enabling UFW, so a bad rule does
not lock you out:

```sh
sudo apt update
sudo apt upgrade -y
sudo apt install -y ufw certbot
sudo ufw allow from YOUR.ADMIN.IP.ADDRESS to any port 22 proto tcp
sudo ufw allow from TEST.CLIENT.CIDR to any port 7697 proto tcp
sudo ufw allow from TEST.CLIENT.CIDR to any port 7697 proto udp
sudo ufw allow 80/tcp                    # remove after Certbot
sudo ufw enable
sudo ufw status numbered
sudo timedatectl set-ntp true
sudo timedatectl status
```

Replace `YOUR.ADMIN.IP.ADDRESS` and `TEST.CLIENT.CIDR` with real narrow values;
do not use those strings literally. The EC2 security group **and** UFW must
allow a connection. When Certbot is done, run `sudo ufw delete allow 80/tcp`
and remove the security-group TCP 80 rule.

## 4. Obtain and install the test bundle safely

There is no released dedicated-hosting bundle yet. Until one exists, obtain a
reviewed archive from a trusted local build/release operator over a private
transfer, then upload it from your own computer with `scp`. Do not download a
random archive or execute a URL-piped installer. When a GitHub release exists,
download the matching `hermec-server-linux-amd64.tar.gz` (or `arm64`) and its
published `SHA256SUMS.txt` from that release, not from an untrusted mirror.

Verify the archive against the checksum before extracting it. Example, with the
expected checksum file obtained from the same trusted release:

```sh
sha256sum -c SHA256SUMS.txt --ignore-missing
```

Upload the already-verified archive and checksum, then verify again on the VPS
(the upload account should be `ubuntu`, not root):

```sh
scp -i /safe/path/hermec-task6.pem hermec-server-linux-amd64.tar.gz SHA256SUMS.txt \
  ubuntu@hermec-test.example.com:~/
ssh -i /safe/path/hermec-task6.pem ubuntu@hermec-test.example.com
mkdir -p ~/hermec-bundle && tar -xzf ~/hermec-server-linux-amd64.tar.gz -C ~/hermec-bundle
cd ~/hermec-bundle
sha256sum -c ~/SHA256SUMS.txt --ignore-missing
sudo ./install.sh
```

This writes the files to the remote home directory. The installer creates `/var/lib/hermec`, its service identity, and a one-time
bootstrap join password. Save that password directly into a password manager;
it is shown once and the server stores only a derived proof key. Never put it
in a command argument, environment variable, history, HERMEC, or a log.

Set the generated config's DNS name and public IP. Preserve `password_key` and
all other generated secret material:

```sh
sudoedit /var/lib/hermec/server.toml
```

Set these existing values to your real names/addresses:

```toml
public_dns = "hermec-test.example.com"
public_ip = "203.0.113.10"
```

The generated dedicated config already uses `addr = ":7697"`, `media_port =
7697`, `public = true`, `discoverable = false`, and TLS paths
`/var/lib/hermec/tls/fullchain.pem` and `/var/lib/hermec/tls/privkey.pem`.

## 5. Issue TLS and deploy it safely

Confirm the `A` record points to this instance and that public TCP 80 is open
in both UFW and the security group. Certbot standalone briefly listens on port
80:

```sh
sudo certbot certonly --standalone -d hermec-test.example.com
```

Install a deploy hook that copies the renewed files into HERMEC's private TLS
directory at mode 0600, with the service account ownership, then restarts the
service. The hook runs after a successful issuance/renewal; it does not expose
keys in a shell argument or log:

```sh
sudo install -d -m 0755 /etc/letsencrypt/renewal-hooks/deploy
sudo tee /etc/letsencrypt/renewal-hooks/deploy/hermec-tls >/dev/null <<'EOF'
#!/bin/sh
set -eu
install -o hermec -g hermec -m 0600 /etc/letsencrypt/live/hermec-test.example.com/fullchain.pem /var/lib/hermec/tls/fullchain.pem
install -o hermec -g hermec -m 0600 /etc/letsencrypt/live/hermec-test.example.com/privkey.pem /var/lib/hermec/tls/privkey.pem
systemctl restart hermec-server
EOF
sudo chmod 0755 /etc/letsencrypt/renewal-hooks/deploy/hermec-tls
sudo /etc/letsencrypt/renewal-hooks/deploy/hermec-tls
sudo ufw delete allow 80/tcp
```

Also remove the EC2 security-group TCP 80 rule. Before each standalone renewal,
temporarily add it in both places, run `sudo certbot renew`, then remove it.
Test that procedure before relying on it. Do not copy `/etc/letsencrypt` keys
into backups, chat, or diagnostics.

Validate the configuration and start the service:

```sh
sudo -u hermec /usr/local/bin/hermec-server -config /var/lib/hermec/server.toml -check
sudo systemctl enable --now hermec-server
sudo systemctl status hermec-server --no-pager
sudo journalctl -u hermec-server -n 100 --no-pager
```

Record the server ID printed by `-check` privately. A passing `-check` proves
local configuration only; it is not remote acceptance. A new group can join at
`wss://hermec-test.example.com:7697/` using the server ID and bootstrap password
through separate trusted channels. For moving an existing founder-managed group,
follow the [founder migration sequence](dedicated-hosting.md#moving-an-existing-group-to-this-vps),
not a copied private key or a fresh bootstrap password.

## Task 6 remote checklist and evidence

This is the exact outstanding remote gate. It is **unrun** as of this guide;
do not mark any row passed without retained real-world evidence.

- Fresh disposable-VPS install; DNS/TLS; join; two-way text/audio; camera and
  selected-screen sharing; restart persistence; backup/restore; update/rollback;
  blocked-UDP diagnosis; and host failover/recovery.
- AWS gate: security group permits required TCP **and** UDP, a real remote
  friend/client connects, and no project infrastructure is used.
- Use a trusted public server and **two independently administered remote
  VPSes/networks**. Pin the server ID, create separate verifier identities,
  admit them, exchange only their public fingerprints, and run the bundled
  `hermec-verify` on both at the same time as specified in the
  [remote verifier instructions](dedicated-hosting.md#remote-reachability-verifier).
  Retain only its mode-0600 safe diagnostic reports plus operator/server
  evidence; do not retain passwords, private identities, TLS keys, SDP, or ICE
  candidates in logs.
- Confirm both verifier reports succeed and contain expected peer frames.
  CI, loopback tests, a healthy systemd unit, and `-check` do not prove internet
  reachability. Preserve the evidence and only release after the plan's gates
  are actually approved.

### Troubleshooting: TCP works but calls do not

If a desktop client joins/chat works but voice does not, do not “fix” it by
opening random ports. HERMEC's default public config needs TCP 7697 for WSS
signaling **and UDP 7697** for WebRTC media. Check the EC2 security group and
UFW for the same intended client source, verify `media_port = 7697` and
`public_ip` is the Elastic IP, then inspect `journalctl -u hermec-server -f`.
Also check the client network/NAT permits UDP and outbound UDP replies. The
verifier's `NEGOTIATION_UDP` is intentionally broad: it can indicate UDP,
firewall, NAT, or media negotiation, not a certificate problem. DNS/certificate
or trust failures are instead in the verifier's `URL_TLS` class.

## Are app features missing?

The backup defect is fixed. There is no known application feature blocker before
the remote test; the remaining gaps before availability are validation-only:
real disposable-VPS/AWS evidence, remote reciprocal media proof, and the Task
6 operational scenarios above. Deliberately deferred features include private
networking/Tailscale or Headscale release work, desktop distribution/updater,
UI ban/grant controls, macOS/Linux camera/share publishing, window capture,
shared-screen audio, simulcast, and echo cancellation. These are not reasons
to claim the unreleased remote gate passed.

## Teardown: prevent surprise charges

After evidence is captured and secrets/backups are handled according to the
[detailed backup guidance](dedicated-hosting.md#backup-update-and-troubleshooting):

1. Remove the DNS `A` record (or change it deliberately), then stop and
   **terminate** the EC2 instance. “Stop” still leaves EBS and other billable
   resources; termination is the disposable-test end.
2. Check whether the 20 GiB EBS volume was deleted with termination. Delete it
   if it is not needed, or create/tag a deliberately retained encrypted snapshot
   and then delete the volume. Snapshots cost money until deleted.
3. Disassociate and **release** the Elastic IP. Verify no other Elastic IPs,
   EBS volumes, snapshots, AMIs, load balancers, NAT gateways, or test DNS
   records remain.
4. Leave the AWS Budget active and revisit Billing/Cost Explorer after charges
   have had time to appear. Delete stale alarms/resources only after preserving
   the minimum approved Task 6 evidence.
