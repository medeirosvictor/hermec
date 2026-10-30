#!/bin/sh
set -eu

if [ "$(id -u)" -ne 0 ]; then
  echo "run this test as root (for example: sudo $0)" >&2
  exit 1
fi

HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM

systemctl="$tmp/systemctl"
cat >"$systemctl" <<'EOF'
#!/bin/sh
[ "$1" = is-active ] && exit 1
exit 0
EOF
chmod 0755 "$systemctl"

assert_mode() {
  [ "$(stat -c '%a' "$1")" = "$2" ] || {
    echo "unsafe mode for $1" >&2
    exit 1
  }
}

backup_and_restore() {
  name=$1
  transition_state=${2:-}
  readiness=${3:-}
  state="$tmp/$name-state"
  archive="$tmp/$name.tar.gz"
  restore="$tmp/$name-restore"
  mkdir -p "$state/logs" "$state/tls"
  chmod 0700 "$state" "$state/logs" "$state/tls"
  for record in server.toml hermec-server.key chain.jsonl history.jsonl; do
    printf '%s\n' "$record" >"$state/$record"
    chmod 0600 "$state/$record"
  done
  # These are intentionally not part of the default archive.
  printf 'log\n' >"$state/logs/server.log"
  printf 'private TLS key\n' >"$state/tls/privkey.pem"
  chmod 0600 "$state/tls/privkey.pem"
  if [ -n "$transition_state" ]; then
    printf '{"state":"%s"}\n' "$transition_state" >"$state/transition.json"
    chmod 0600 "$state/transition.json"
  fi
  if [ -n "$readiness" ]; then
    printf '{"attestation":"%s"}\n' "$readiness" >"$state/readiness.json"
    chmod 0600 "$state/readiness.json"
  fi

  HERMEC_STATE="$state" HERMEC_SYSTEMCTL="$systemctl" "$HERE/backup.sh" "$archive" >/dev/null
  assert_mode "$archive" 600
  tar -tzf "$archive" >"$tmp/$name.contents"
  for record in server.toml hermec-server.key chain.jsonl history.jsonl; do
    grep -Fx "$record" "$tmp/$name.contents" >/dev/null
  done
  if [ -n "$transition_state" ]; then
    grep -Fx transition.json "$tmp/$name.contents" >/dev/null
  else
    ! grep -Fx transition.json "$tmp/$name.contents"
  fi
  if [ -n "$readiness" ]; then
    grep -Fx readiness.json "$tmp/$name.contents" >/dev/null
  else
    ! grep -Fx readiness.json "$tmp/$name.contents"
  fi
  ! grep -Eq '^(logs|tls)(/|$)' "$tmp/$name.contents"

  # The documented restore is extraction into a new matching state directory.
  mkdir -m 0700 "$restore"
  tar -xzf "$archive" -C "$restore"
  for record in server.toml hermec-server.key chain.jsonl history.jsonl; do
    cmp "$state/$record" "$restore/$record"
    assert_mode "$restore/$record" 600
  done
  if [ -n "$transition_state" ]; then
    cmp "$state/transition.json" "$restore/transition.json"
    assert_mode "$restore/transition.json" 600
  fi
  if [ -n "$readiness" ]; then
    cmp "$state/readiness.json" "$restore/readiness.json"
    assert_mode "$restore/readiness.json" 600
  fi
}

# Each durable transition state must survive restoration; readiness belongs to
# the active source and may coexist with its transition journal.
backup_and_restore prepared prepared ready
backup_and_restore fenced fenced ready
backup_and_restore committed committed ready
# A server that never took part in a cutover has no optional records.
backup_and_restore no-transition
