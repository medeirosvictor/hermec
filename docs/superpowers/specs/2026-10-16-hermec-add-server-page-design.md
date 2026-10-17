# HERMEC: Add a Server page (Create my own / Join) — design

Date: 2026-10-16. Status: design approved in chat by Victor; this spec is for his review.
Learner directive: place things where Discord does. Goal: friends can run and join servers without command-line flags.

## 1. Problem

- Hosting a server friends can reach requires running `hermec-server` from a terminal. The GUI's `-local` server listens on `127.0.0.1` with a random port, so only this machine can reach it, and its address changes every run.
- A server tile appears only after a successful first connect. While a first connect is in progress, or after it fails, the rail shows nothing.
- The DISCOVERED list's selection (Up/Down) does not fill the server address field.

## 2. Flow (Discord's "Add a Server")

The **+** tile on the rail opens the **Add a Server** page. So does the first screen when there are no saved servers. The page offers two choices:

1. **Create my own.** A form with three fields:
   - **server name** (required; cleaned with `state.CleanName`, at most 64 runes);
   - **password** (optional);
   - **port** (default 7697).

   **Create** saves the hosted-server config, starts the server inside the app, and connects to it (section 3).
2. **Join a server.** Today's connect form (name and server address) plus the DISCOVERED list. **Up/Down** moves the DISCOVERED selection *and* writes the selected server's address into the address field. Clicking a row does the same, and Enter joins.

Esc on either form returns to the two choices, and Esc there returns to the previous view. When there are no servers, the two choices are the home screen and there is nothing to return to.

## 3. Hosting from the app

- **Config file:** `<user config dir>/hermec/hosted.toml`, written with the server's existing config format (`server.Config` / `LoadConfig` / `MarshalTOML`):
  - `Addr ":<port>"` (all interfaces, so friends on Tailscale or the LAN can reach it);
  - `ServerName`, `Password`;
  - `Channels ["general"]`, `VoiceChannels ["voice"]`;
  - `Discoverable true`;
  - the builtin roles, with the hosting identity's fingerprint as admin, via the config's existing role-assignment mechanism. If none exists, the plan adds the smallest one.

  `AllowLoopbackICE` stays false; it breaks remote voice. One hosted server per app.
- **Lifetime:** the GUI owns the server through a handle. The server runs while the app is open and shuts down cleanly when the window closes (same 3 s grace as `-local`).
- **Auto-start:** at launch, if `hosted.toml` exists, the app starts the server before loading saved servers, then connects to it like any saved server. This is Victor's call (2026-10-16).
- **Own connection:** the saved entry uses the stable key `hermec://hosted`, mapped at runtime to `ws://127.0.0.1:<port>/` (same idea as `hermec://local`). Its label is the server name, and its tile reads like any other server's.
- **Share address:** the Create page and the hosted tile's right-click menu show **Invite address**. That is the Tailscale address if one is found, otherwise the LAN address, plus `:<port>`, with a **copy** action that puts it on the clipboard.
- **Stop or start:** the hosted tile's right-click menu gains **Stop hosting** (shuts the server down; the tile goes into the failed state, section 4) or **Start hosting** when stopped. **Leave Server** on the hosted tile also stops it and deletes `hosted.toml`, after a confirm dialog that says so.
- **Port in use:** if the server can't listen, the Create page shows `port <n> is in use (is hermec-server running?)` and keeps the form, so the port can be changed. If it happens at auto-start, the hosted tile shows the failed state with that message in its tooltip.
- **Firewall:** the first listen on all interfaces triggers the Windows Defender Firewall prompt once per exe path. The help section and the Create page carry one line: "Windows will ask to allow HERMEC on your network — choose Allow so friends can connect."
- **`-local` is unchanged:** a developer flag, loopback only, random port, `hermec://local`.

## 4. Tiles while connecting and after failure

- **Immediate tile:** pressing Join or Create adds the server's tile to the rail at once, **greyed and pulsing** (the existing reconnecting look), and the view switches to it.
  - A tile that has not yet connected once is *pending*. It is kept in memory but not written to `servers.toml` until its first successful connect.
  - The rail, its hit areas and its tooltips must handle pending entries. Today they index `g.servers` by tile position.
- **Failed state:** whenever a tile's most recent dial failed and it is not live (a first connect that failed, a reconnect attempt that failed, or a stopped hosted server), the tile shows two badges:
  - a small **red ✕** at its **bottom-right**;
  - a **retry** badge, a circular arrow (↻, drawn as a vector icon like the other rail marks), at its **bottom-left**.

  Clicking the retry badge redials immediately and resets the backoff; for a stopped hosted server, it starts hosting. The tile's tooltip shows the error text under the server name. Red is the colour the menu already uses for Leave Server (its warn style).
- **First-connect failures no longer drop the session silently:** the session stays, with its tile in the failed state, and the viewed pane shows the error and "click ↻ to retry". Right-click offers Retry and Leave Server.
- A live tile clears both badges. Reconnecting tiles between attempts keep today's pulse, and gain the badges only after an attempt fails.

## 5. Out of scope

Host migration (whoever is online hosts) belongs to Plans A/B. Also out of scope: more than one hosted server, editing hosted channels or roles in the GUI, TLS for hosted servers, and a password prompt when joining (if the existing form lacks one, the plan notes it as a follow-up).

## 6. Testing

- **Headless unit tests** in `ui/state` and `ui`:
  - the hosted config round trip (write, then load);
  - the fingerprint-as-admin mapping;
  - the auto-start decision (with and without `hosted.toml`);
  - the port-in-use error message;
  - Up/Down filling the address field;
  - pending entries: shown on the rail but not saved, saved on first success, and removed by Leave;
  - failed-state facts: badge flags from session state, the retry hit rect and the error in the tooltip;
  - the hosted key ↔ URL mapping.
- **Integration test:** start a hosted server from the config on port 0, connect two clients, check presence, then shut it down.
- **Victor's real-mouse checklist:**
  1. Create a server from the app.
  2. A friend joins over Tailscale using the copied invite address.
  3. Close and reopen the app: the server comes back and the tile reconnects.
  4. Start `hermec-server` on 7697 first and see the port-in-use error.
  5. Join a bad address and see the red ✕ and ↻, then click ↻.
  6. Up/Down in DISCOVERED fills the field.
  7. Stop hosting, then Start hosting.
  8. Leave the hosted server and check `hosted.toml` is gone.
