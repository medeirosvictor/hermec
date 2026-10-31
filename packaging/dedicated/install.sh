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
if [ ! -e "$STATE" ]; then
  install -d -o hermec -g hermec -m 0700 "$STATE"
  if command -v runuser >/dev/null 2>&1; then
    runuser -u hermec -- "$BIN" -init-dedicated "$STATE"
  else
    su -s /bin/sh hermec -c "exec '$BIN' -init-dedicated '$STATE'"
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
