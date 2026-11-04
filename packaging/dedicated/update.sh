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
TAG=${HERMEC_TAG:-}
UNSIGNED=${HERMEC_UNSIGNED:-}
ALLOW_DOWNGRADE=${HERMEC_ALLOW_DOWNGRADE:-}
# The verifier is the copy install.sh put in a root-owned directory, never a
# tool from the archive being verified. The test-only variables exist so the
# test suite can run without root; production leaves them unset.
LIBDIR=/usr/local/lib/hermec
RELEASE_TOOL=${HERMEC_TEST_RELEASE_TOOL:-$LIBDIR/hermec-release}
RELEASE_PUB=${HERMEC_TEST_RELEASE_PUB:-}
ACTION=${1:-}
NEW=${2:-}
STAGED=
WORK=

cleanup() {
  [ -z "$STAGED" ] || rm -f "$STAGED"
  [ -z "$WORK" ] || rm -rf "$WORK"
}
trap cleanup EXIT
trap 'exit 1' INT TERM HUP

die() {
  echo "$*" >&2
  exit 1
}

# parse_tag sets PARSED to "MAJOR MINOR PATCH" for a strict vX.Y.Z tag, else
# fails (the same rule the desktop updater applies).
parse_tag() {
  case "$1" in v*) ;; *) return 1 ;; esac
  rest=${1#v}
  case "$rest" in *[!0-9.]*) return 1 ;; esac
  major=${rest%%.*}
  rest=${rest#*.}
  case "$rest" in *.*) ;; *) return 1 ;; esac
  minor=${rest%%.*}
  patch=${rest#*.}
  case "$patch" in *.*) return 1 ;; esac
  for part in "$major" "$minor" "$patch"; do
    case "$part" in ''|?????????*) return 1 ;; esac
  done
  PARSED="$major $minor $patch"
}

# tag_is_newer succeeds when the parsed triple $1 $2 $3 is strictly greater
# than $4 $5 $6.
tag_is_newer() {
  [ "$1" -ne "$4" ] && { [ "$1" -gt "$4" ]; return; }
  [ "$2" -ne "$5" ] && { [ "$2" -gt "$5" ]; return; }
  [ "$3" -gt "$6" ]
}

# check_not_downgrade refuses a release that is not newer than the installed
# server, so an old (validly signed) release cannot be replayed.
# HERMEC_ALLOW_DOWNGRADE=1 is the deliberate escape hatch.
check_not_downgrade() {
  [ -x "$BIN" ] || return 0
  installed=$("$BIN" -version 2>/dev/null) || installed=
  installed=${installed##* }
  if parse_tag "$installed"; then
    have=$PARSED
    parse_tag "$TAG"
    # shellcheck disable=SC2086
    if tag_is_newer $PARSED $have; then return 0; fi
    reason="$TAG is not newer than the installed $installed"
  else
    reason="the installed version ($installed) is not a release tag, so $TAG cannot be compared"
  fi
  if [ "$ALLOW_DOWNGRADE" = 1 ]; then
    echo "warning: $reason; continuing because HERMEC_ALLOW_DOWNGRADE=1" >&2
    return 0
  fi
  die "$reason (set HERMEC_ALLOW_DOWNGRADE=1 to install it anyway)"
}

# verify_release checks a signed release before anything is staged or stopped:
# the signature of the checksums must verify for HERMEC_TAG under the
# installed verifier, the tag must be newer than the installed server, and the
# archive must match its line of the signed checksums. The archive is copied
# into a private directory first and that copy is the one hashed and
# extracted, so it cannot change between the check and the use.
verify_release() {
  [ -f "$SUMS" ] && [ -f "$SUMS.sig" ] || die "missing $SUMS or $SUMS.sig"
  [ -f "$ARCHIVE" ] || die "HERMEC_ARCHIVE must be the release .tar.gz"
  parse_tag "$TAG" || die "HERMEC_TAG must be a release tag such as v1.2.3"
  [ -x "$RELEASE_TOOL" ] || die "release verifier not found: $RELEASE_TOOL (install.sh puts it there)"
  if [ -z "${HERMEC_TEST_RELEASE_TOOL:-}" ] && [ "$(id -u)" -eq 0 ]; then
    unsafe=$(find "$LIBDIR" "$RELEASE_TOOL" -maxdepth 0 \( ! -user root -o -perm -0020 -o -perm -0002 \) -print 2>/dev/null) || unsafe=$LIBDIR
    [ -z "$unsafe" ] || die "$LIBDIR and the verifier in it must be owned by root and not writable by others"
  fi
  if [ -n "$RELEASE_PUB" ]; then
    "$RELEASE_TOOL" verify -tag "$TAG" -pub "$RELEASE_PUB" "$SUMS" >/dev/null || die "release signature does not verify for $TAG"
  else
    "$RELEASE_TOOL" verify -tag "$TAG" "$SUMS" >/dev/null || die "release signature does not verify for $TAG"
  fi
  echo "signature ok for tag $TAG"
  check_not_downgrade
  WORK=$(mktemp -d)
  chmod 0700 "$WORK"
  cp "$ARCHIVE" "$WORK/archive"
  name=$(basename "$ARCHIVE")
  want=$(WANT_NAME=$name awk '$2 == ENVIRON["WANT_NAME"] || $2 == "*" ENVIRON["WANT_NAME"] { print $1 }' "$SUMS")
  case "$want" in
    ????????????????????????????????????????????????????????????????) ;;
    *) die "$name is not listed exactly once in the signed checksums" ;;
  esac
  have=$(sha256sum "$WORK/archive" | awk '{ print $1 }')
  [ "$have" = "$want" ] || die "$name does not match its signed checksum"
}

# stage_file creates the empty staged file next to $BIN.
stage_file() {
  mkdir -p "$(dirname "$BIN")"
  STAGED=$(mktemp "$(dirname "$BIN")/.hermec-server.candidate.XXXXXX")
}

# seal_staged makes the staged file read-only (and root-owned as root), so the
# file that is validated is the file that is installed.
seal_staged() {
  if [ "$(id -u)" -eq 0 ]; then chown root:root "$STAGED"; fi
  chmod 0555 "$STAGED"
}

# stage_candidate copies a caller-supplied binary (HERMEC_UNSIGNED=1 only).
stage_candidate() {
  source=$1
  [ -f "$source" ] || die "candidate is not a file: $source"
  stage_file
  cat "$source" > "$STAGED"
  seal_staged
}

# stage_from_archive extracts ./hermec-server, which must sit exactly at the
# archive root, from the verified copy straight into the staged file.
stage_from_archive() {
  members=$(tar -tzf "$WORK/archive" | awk '$0 == "./hermec-server" || $0 == "hermec-server" { print }')
  case "$members" in
    ./hermec-server|hermec-server) ;;
    *) die "the archive must contain hermec-server exactly once at its root" ;;
  esac
  stage_file
  # ulimit (where supported) caps the output at 256 MiB (512-byte blocks).
  (ulimit -f 524288 2>/dev/null; tar -xzOf "$WORK/archive" "$members" > "$STAGED") || die "could not extract hermec-server from the archive"
  [ -s "$STAGED" ] || die "hermec-server in the archive is empty or not a regular file"
  seal_staged
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
  # A signed release must install exactly the version that was signed.
  if [ "$UNSIGNED" != 1 ] && [ "$version" != "hermec-server $TAG" ]; then
    die "candidate reports \"$version\", not hermec-server $TAG"
  fi
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
    if [ "$UNSIGNED" = 1 ]; then
      [ -n "$NEW" ] || { echo "usage: HERMEC_UNSIGNED=1 $0 update /path/to/hermec-server" >&2; exit 2; }
      echo "WARNING: HERMEC_UNSIGNED=1: installing $NEW with NO signature or checksum verification." >&2
      echo "WARNING: only do this for a binary you built or checked yourself." >&2
    else
      if [ -z "$SUMS" ] || [ -z "$ARCHIVE" ] || [ -z "$TAG" ] || [ -n "$NEW" ]; then
        echo "usage: HERMEC_SUMS=SHA256SUMS.txt HERMEC_ARCHIVE=hermec-server-linux-<arch>.tar.gz HERMEC_TAG=vX.Y.Z $0 update" >&2
        echo "       (HERMEC_UNSIGNED=1 $0 update /path/to/hermec-server skips verification)" >&2
        exit 2
      fi
      verify_release
    fi
    # Validate the staged copy against the real persistent state before
    # disrupting a live service; this mirrors the unit's ExecStartPre check.
    if [ "$UNSIGNED" = 1 ]; then stage_candidate "$NEW"; else stage_from_archive; fi
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
    echo "usage: $0 update | rollback   (see docs/dedicated-hosting.md)" >&2
    exit 2
    ;;
esac
