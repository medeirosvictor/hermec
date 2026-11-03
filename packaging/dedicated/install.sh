#!/bin/sh
set -eu

if [ "$(id -u)" -ne 0 ]; then
  echo "run this installer as root" >&2
  exit 1
fi
HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
BIN=${HERMEC_BIN:-/usr/local/bin/hermec-server}
STATE=${HERMEC_STATE:-/var/lib/hermec}
UNIT=${HERMEC_UNIT:-/etc/systemd/system/hermec-server.service}
SYSTEMCTL=${HERMEC_SYSTEMCTL:-systemctl}

if ! id hermec >/dev/null 2>&1; then
  useradd --system --home-dir "$STATE" --shell /usr/sbin/nologin hermec
fi
install -m 0755 "$HERE/hermec-server" "$BIN"
# The updater and the release verifier live in a root-owned directory and are
# the only copies update.sh trusts: it never runs a tool from a freshly
# downloaded archive. Keep LIB in step with LIBDIR in update.sh.
LIB=/usr/local/lib/hermec
install -d -o root -g root -m 0755 "$LIB"
install -o root -g root -m 0755 "$HERE/hermec-release" "$LIB/hermec-release"
install -o root -g root -m 0755 "$HERE/update.sh" "$LIB/update.sh"
if [ ! -e "$STATE" ]; then
  install -d -o hermec -g hermec -m 0700 "$STATE"
  # Bootstrap as the service user. On failure remove the directory created
  # above, so a rerun bootstraps again instead of skipping an empty state.
  if command -v runuser >/dev/null 2>&1; then
    runuser -u hermec -- "$BIN" -init-dedicated "$STATE" || { rm -rf "$STATE"; exit 1; }
  else
    su -s /bin/sh hermec -c "exec '$BIN' -init-dedicated '$STATE'" || { rm -rf "$STATE"; exit 1; }
  fi
fi
chown -R hermec:hermec "$STATE"
chmod 0700 "$STATE" "$STATE/tls" "$STATE/logs" "$STATE/backups"
install -m 0644 "$HERE/hermec-server.service" "$UNIT"
"$SYSTEMCTL" daemon-reload

echo "Installed HERMEC. Before starting:"
echo "  1. put fullchain.pem and privkey.pem in $STATE/tls"
echo "  2. set public_dns to the certificate DNS name (and public_ip for 1:1 NAT) in $STATE/server.toml"
echo "  3. check the installed version: $BIN -version"
echo "  4. run: $BIN -config $STATE/server.toml -check"
echo "  5. run: systemctl enable --now hermec-server"
echo "To update later, verify the new release with $LIB/hermec-release and run $LIB/update.sh"
echo "(see docs/dedicated-hosting.md, \"Updating a signed release\")."
