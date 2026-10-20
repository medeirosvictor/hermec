#!/usr/bin/env bash
# Build HERMEC for macOS from the latest release tag, with the version stamped
# in, so the update banner works. Run it from the repo root:
#   scripts/build-mac.sh
set -euo pipefail

cd "$(dirname "$0")/.."

git fetch --tags

# The newest release tag that is on main.
tag="$(git describe --tags --abbrev=0 origin/main)"
echo "Building HERMEC $tag"

# This leaves you on a detached HEAD at the tag, not on main.
git checkout "$tag"

go build -ldflags "-X github.com/medeirosvictor/hermec/version.Version=$tag" -o hermec ./cmd/hermec

echo
echo "Built ./hermec ($tag)."
echo "Run it:  ./hermec"
echo "Update:  run scripts/build-mac.sh again"
echo "Back to the development branch: git checkout main"
