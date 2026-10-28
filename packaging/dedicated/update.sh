#!/bin/sh
set -eu

if [ "$(id -u)" -ne 0 ] && [ "${HERMEC_ALLOW_NON_ROOT:-}" != "1" ]; then
  echo "run this command as root" >&2
  exit 1
fi
BIN=${HERMEC_BIN:-/usr/local/bin/hermec-server}
PREVIOUS=${HERMEC_PREVIOUS:-/usr/local/lib/hermec/hermec-server.previous}
STATE=${HERMEC_STATE:-/var/lib/hermec}
CONFIG=${HERMEC_CONFIG:-$STATE/server.toml}
SYSTEMCTL=${HERMEC_SYSTEMCTL:-systemctl}
SERVICE=${HERMEC_SERVICE:-hermec-server}
HEALTH_ATTEMPTS=${HERMEC_HEALTH_ATTEMPTS:-5}
HEALTH_DELAY=${HERMEC_HEALTH_DELAY:-1}
ACTION=${1:-}
NEW=${2:-}

validate_candidate() {
  source=$1
  [ -f "$source" ] && [ -x "$source" ] || { echo "candidate is not an executable file: $source" >&2; exit 1; }
  version=$("$source" -version) || { echo "candidate version check failed" >&2; exit 1; }
  case "$version" in
    hermec-server\ ?*) ;;
    *) echo "candidate did not report a HERMEC server version" >&2; exit 1 ;;
  esac
  "$source" -config "$CONFIG" -check
}

wait_for_active() {
  attempt=0
  while [ "$attempt" -lt "$HEALTH_ATTEMPTS" ]; do
    if "$SYSTEMCTL" is-active --quiet "$SERVICE"; then
      return 0
    fi
    attempt=$((attempt + 1))
    [ "$attempt" -lt "$HEALTH_ATTEMPTS" ] && sleep "$HEALTH_DELAY"
  done
  return 1
}

install_binary() {
  source=$1
  mkdir -p "$(dirname "$BIN")" "$(dirname "$PREVIOUS")"
  tmp="$BIN.new.$$"
  install -m 0755 "$source" "$tmp"
  mv -f "$tmp" "$BIN"
}

restore_previous() {
  echo "new server failed health check; rolling back" >&2
  [ -f "$PREVIOUS" ] || return 1
  install -m 0755 "$PREVIOUS" "$BIN"
  "$SYSTEMCTL" start "$SERVICE"
  if ! wait_for_active; then
    echo "rollback server failed health check" >&2
  fi
  return 1
}

case "$ACTION" in
  update)
    [ -n "$NEW" ] || { echo "usage: $0 update /path/to/hermec-server" >&2; exit 2; }
    # Validate the downloaded HERMEC binary and its real persistent state before
    # disrupting a live service; this mirrors the unit's ExecStartPre check.
    validate_candidate "$NEW"
    was_active=0
    if "$SYSTEMCTL" is-active --quiet "$SERVICE"; then was_active=1; "$SYSTEMCTL" stop "$SERVICE"; fi
    if [ -f "$BIN" ]; then install -m 0755 "$BIN" "$PREVIOUS"; fi
    install_binary "$NEW"
    if [ "$was_active" -eq 1 ]; then
      "$SYSTEMCTL" start "$SERVICE" || restore_previous
      wait_for_active || restore_previous
    fi
    ;;
  rollback)
    [ -f "$PREVIOUS" ] || { echo "no previous binary at $PREVIOUS" >&2; exit 1; }
    was_active=0
    if "$SYSTEMCTL" is-active --quiet "$SERVICE"; then was_active=1; "$SYSTEMCTL" stop "$SERVICE"; fi
    swap="$PREVIOUS.swap.$$"
    if [ -f "$BIN" ]; then install -m 0755 "$BIN" "$swap"; fi
    install -m 0755 "$PREVIOUS" "$BIN"
    if [ -f "$swap" ]; then mv -f "$swap" "$PREVIOUS"; fi
    if [ "$was_active" -eq 1 ]; then
      "$SYSTEMCTL" start "$SERVICE"
      wait_for_active || { echo "rolled-back server failed health check" >&2; exit 1; }
    fi
    ;;
  *)
    echo "usage: $0 update /path/to/hermec-server | rollback" >&2
    exit 2
    ;;
esac
