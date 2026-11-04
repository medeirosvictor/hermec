# Troubleshooting

## I can't hear anyone, or they can't hear me

- Check the `input` and `output` rows in Settings (**F10**); they switch devices
  immediately, even mid-call. If a new device fails, the old one stays and a
  status says so.
- Check you are not muted (**Ctrl+M**) and look at your level meter in the call
  bar. Per-person volume (right-click a person) may be at 0%.
- On Linux you need PulseAudio or PipeWire. On macOS, allow the microphone for
  Terminal in System Settings → Privacy & Security → Microphone.
- Use headphones: there is no echo cancellation, so speakers feed audio back into
  the microphone.
- If chat works but calls do not, see the next section.

## Chat works but calls do not (UDP blocked)

Voice and video use one UDP port, by default the same number as the server's TCP
port (7697), or `media_port` when set. TCP-only rules let signaling through but
do not make calls work. Check that UDP is allowed:

- on the host's firewall (Windows Firewall, `ufw`, nftables) and, for a VPS, the
  provider's firewall or security group;
- on the router, if you forward ports (forward TCP and UDP 7697 and set
  `public_ip`);
- in the Tailscale access controls, if you narrowed them (allow `udp:7697` as
  well as `tcp:7697`, see [Security](security.md#tailscale-grants)).

The connection-quality panel (click the signal bars) shows ping, jitter and
concealment. For dedicated servers there is a reachability verifier and a
TCP-works-but-calls-don't section in
[Dedicated hosting](dedicated-hosting.md#remote-reachability-verifier) and
[the AWS guide](aws-ec2-from-zero.md#troubleshooting-tcp-works-but-calls-do-not).

## Windows Firewall prompts

**Expected:** the real app prompts once for firewall access (UDP socket for LAN
discovery and voice media relay). Choose **Allow** on private networks so
friends can connect.

**Denying the prompt:** loopback (localhost, 127.0.0.1) and Tailscale still work.
LAN discovery, and anyone else on your LAN reaching a server you host, are the
only things that require the firewall exception. The machine hosting the server
must have its server executable (`hermec-server.exe`, or `hermec.exe` when using
`-local`) **Allowed**, or LAN peers cannot connect or hear voice; clients that
only join someone else's server need no inbound exception. To limit which
networks may reach you, see the PowerShell snippet in
[Security](security.md#known-gaps).

**During development:** running with `go run` or `go test` creates temporary
executables, each triggering a new prompt; build a stable one instead (see
[Development](development.md#run-locally)).

## Update required, or version mismatch

Every client and host in a group must run the same build; the server rejects any
other with `version_mismatch`. If the server's build is newer than yours, the
banner offers "update now" (**Ctrl+U**) on Windows and Linux. On macOS, run
`scripts/build-mac.sh` again. If the server is older than you, ask its host to
update (dedicated servers use the installed `update.sh`, see
[Dedicated hosting](dedicated-hosting.md#updating-a-signed-release)). If the
in-app update fails or the install folder is not writable, the banner links to
the Releases page: download the build by hand
([Getting started](getting-started.md#install)).

## The app refuses to connect and says the server changed

The app pins each server's ID the first time you connect and refuses, without
retrying, if it ever changes (see [Security](security.md)). If you did not expect
that, do not connect: confirm with the host that they recreated the server.

## Logs

Each time you start Hermec it writes one log file. The log never contains chat
text or keys.

| System | Folder |
|---|---|
| Windows | `%APPDATA%\hermec\logs` |
| macOS | `~/Library/Application Support/hermec/logs` |
| Linux | `~/.config/hermec/logs` |

Files are named like `hermec-2026-10-19_21-30-05.log`. Hermec keeps the newest
10 and deletes older ones. To find the folder fast, open Settings and click the
row `logs: open folder`; it opens the folder and shows the path. To send a log to
a friend, open the folder and attach the newest file to a message. `hermec -v`
adds log breadcrumbs.

A dedicated server logs to the journal: `journalctl -u hermec-server -f`.
