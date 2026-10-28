#!/bin/sh
set -eu

if [ "$(id -u)" -ne 0 ]; then
  echo "run this command as root" >&2
  exit 1
fi
STATE=${HERMEC_STATE:-/var/lib/hermec}
SYSTEMCTL=${HERMEC_SYSTEMCTL:-systemctl}
SERVICE=${HERMEC_SERVICE:-hermec-server}
DEST=${1:-$STATE/backups/hermec-$(date -u +%Y%m%dT%H%M%SZ).tar.gz}
was_active=0
if "$SYSTEMCTL" is-active --quiet "$SERVICE"; then
  was_active=1
  "$SYSTEMCTL" stop "$SERVICE"
fi
restart() { if [ "$was_active" -eq 1 ]; then "$SYSTEMCTL" start "$SERVICE"; fi; }
trap restart EXIT HUP INT TERM
mkdir -p "$(dirname "$DEST")"
tmp="$DEST.tmp.$$"
tar -czf "$tmp" -C "$STATE" server.toml hermec-server.key chain.jsonl history.jsonl tls ${HERMEC_BACKUP_EXTRA:-}
chmod 0600 "$tmp"
mv -f "$tmp" "$DEST"
echo "$DEST"
