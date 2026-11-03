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
SERVICE_USER=${HERMEC_USER:-hermec}
SUMS=${HERMEC_SUMS:-}
ARCHIVE=${HERMEC_ARCHIVE:-}
RELEASE_TOOL=${HERMEC_RELEASE_TOOL:-hermec-release}
RELEASE_PUB=${HERMEC_RELEASE_PUB:-}
ACTION=${1:-}
NEW=${2:-}
STAGED=

cleanup() {
  [ -z "$STAGED" ] || rm -f "$STAGED"
}
trap cleanup EXIT
trap 'exit 1' INT TERM HUP

# verify_release is run when HERMEC_SUMS is set: the signature of the release
# checksums must verify, the release archive must match a line of them, and the
# candidate must be byte-identical to the hermec-server inside that archive.
# Nothing is staged or stopped before this passes.
verify_release() {
  [ -f "$SUMS" ] && [ -f "$SUMS.sig" ] || { echo "missing $SUMS or $SUMS.sig" >&2; exit 1; }
  [ -f "$ARCHIVE" ] || { echo "HERMEC_SUMS needs HERMEC_ARCHIVE=<the release .tar.gz>" >&2; exit 1; }
  command -v "$RELEASE_TOOL" >/dev/null 2>&1 || { echo "release verifier not found: $RELEASE_TOOL (set HERMEC_RELEASE_TOOL)" >&2; exit 1; }
  if [ -n "$RELEASE_PUB" ]; then
    "$RELEASE_TOOL" verify -pub "$RELEASE_PUB" "$SUMS" >/dev/null || { echo "release signature does not verify" >&2; exit 1; }
  else
    "$RELEASE_TOOL" verify "$SUMS" >/dev/null || { echo "release signature does not verify" >&2; exit 1; }
  fi
  name=$(basename "$ARCHIVE")
  want=$(awk -v n="$name" '$2 == n || $2 == "*" n { print $1 }' "$SUMS")
  case "$want" in
    ????????????????????????????????????????????????????????????????) ;;
    *) echo "$name is not listed exactly once in the signed checksums" >&2; exit 1 ;;
  esac
  have=$(sha256sum "$ARCHIVE" | awk '{ print $1 }')
  [ "$have" = "$want" ] || { echo "$name does not match its signed checksum" >&2; exit 1; }
  tar -xzO ./hermec-server < "$ARCHIVE" | cmp -s - "$NEW" || { echo "candidate is not the hermec-server in $name" >&2; exit 1; }
}

# stage_candidate copies the candidate once into a read-only file next to $BIN,
# so the file that is validated is the file that is installed, whatever later
# happens at the caller's path.
stage_candidate() {
  source=$1
  [ -f "$source" ] || { echo "candidate is not a file: $source" >&2; exit 1; }
  mkdir -p "$(dirname "$BIN")"
  STAGED=$(mktemp "$(dirname "$BIN")/.hermec-server.candidate.XXXXXX")
  cat "$source" > "$STAGED"
  if [ "$(id -u)" -eq 0 ]; then chown root:root "$STAGED"; fi
  chmod 0555 "$STAGED"
}

# run_candidate runs the staged candidate. As root it never runs with root
# privileges: it runs as the service user in a transient, resource-limited
# systemd sandbox that can write only the state directory.
run_candidate() {
  if [ "$(id -u)" -ne 0 ]; then
    "$STAGED" "$@"
    return
  fi
  command -v systemd-run >/dev/null 2>&1 || { echo "systemd-run is required to validate a candidate" >&2; exit 1; }
  systemd-run --quiet --wait --pipe --collect \
    -p User="$SERVICE_USER" -p Group="$SERVICE_USER" \
    -p NoNewPrivileges=yes -p PrivateTmp=yes -p ProtectHome=yes \
    -p ProtectSystem=strict -p ReadWritePaths="$STATE" \
    -p UMask=0077 -p WorkingDirectory="$STATE" -p CapabilityBoundingSet= \
    -p PrivateDevices=yes -p PrivateNetwork=yes \
    -p RuntimeMaxSec=30 -p MemoryMax=512M -p TasksMax=64 \
    -- "$STAGED" "$@"
}

validate_candidate() {
  version=$(run_candidate -version) || { echo "candidate version check failed" >&2; exit 1; }
  case "$version" in
    hermec-server\ ?*) ;;
    *) echo "candidate did not report a HERMEC server version" >&2; exit 1 ;;
  esac
  run_candidate -config "$CONFIG" -check
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

install_staged() {
  chmod 0755 "$STAGED"
  mv -f "$STAGED" "$BIN"
  STAGED=
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
    if [ -n "$SUMS" ]; then verify_release; fi
    # Validate the staged copy against the real persistent state before
    # disrupting a live service; this mirrors the unit's ExecStartPre check.
    stage_candidate "$NEW"
    validate_candidate
    was_active=0
    if "$SYSTEMCTL" is-active --quiet "$SERVICE"; then was_active=1; "$SYSTEMCTL" stop "$SERVICE"; fi
    mkdir -p "$(dirname "$PREVIOUS")"
    if [ -f "$BIN" ]; then install -m 0755 "$BIN" "$PREVIOUS"; fi
    install_staged
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
