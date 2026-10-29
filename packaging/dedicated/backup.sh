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
set -- server.toml hermec-server.key chain.jsonl history.jsonl
# Transition records are created only while dedicated cutover is in progress.
# Old state directories therefore legitimately do not have either file.
for record in transition.json readiness.json; do
  if [ -f "$STATE/$record" ]; then
    set -- "$@" "$record"
  fi
done
# This remains an explicit operator opt-in for additional state paths.
if [ -n "${HERMEC_BACKUP_EXTRA:-}" ]; then
  # HERMEC_BACKUP_EXTRA is a space-separated list of paths supplied by the
  # operator; intentional splitting permits more than one path.
  # shellcheck disable=SC2086
  set -- "$@" $HERMEC_BACKUP_EXTRA
fi
tar -czf "$tmp" -C "$STATE" "$@"
chmod 0600 "$tmp"
mv -f "$tmp" "$DEST"
echo "$DEST"
