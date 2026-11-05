#!/usr/bin/env bash
# Installs a release from local archives through install.sh, then checks
# doctor, update and uninstall, as a user would run them.
#
# Usage: scripts/smoke.sh <dir with archives and SHA256SUMS> <tag>
# The tag is a vX.Y.Z release, installed from the stable channel, or canary,
# installed from the latest channel.

set -euo pipefail

dist="$(cd "$1" && pwd)"
tag="$2"
repo_root="$(cd "$(dirname "$0")/.." && pwd)"
port="${SMOKE_PORT:-8765}"

work="$(mktemp -d)"
trap 'kill "${server_pid:-}" 2>/dev/null || true; rm -rf "$work"' EXIT
export HOME="$work/home"
mkdir -p "$HOME" "$work/releases/download/$tag"
cp "$dist"/*.tar.gz "$dist/SHA256SUMS" "$work/releases/download/$tag/"

python3 -m http.server "$port" --bind 127.0.0.1 --directory "$work" >"$work/server.log" 2>&1 &
server_pid=$!
for _ in $(seq 1 50); do
  curl -fsS "http://127.0.0.1:$port/" >/dev/null 2>&1 && break
  sleep 0.1
done

export PREFACTOR_RELEASE_BASE_URL="http://127.0.0.1:$port/releases/download"
# The stable channel reads releases/latest, which GitHub redirects to the
# newest tag; here it is that tag's directory.
export PREFACTOR_RELEASE_LATEST_BASE_URL="$PREFACTOR_RELEASE_BASE_URL/$tag"
channel=stable
if [ "$tag" = canary ]; then
  channel=latest
fi

prefactor="$HOME/.prefactor/bin/prefactor"
check_doctor() {
  "$prefactor" doctor | tee "$work/doctor.log"
  grep -q "^channel: $channel$" "$work/doctor.log"
  grep -q "^managedBinaryExists: yes$" "$work/doctor.log"
}

bash "$repo_root/install.sh" "$channel"
"$prefactor" --version
"$prefactor" version
check_doctor

# update finds the tag from where the download was served.
"$prefactor" update
check_doctor
grep -q "^resolvedTag: $tag$" "$work/doctor.log"

"$prefactor" uninstall
[ ! -e "$prefactor" ] || { echo "uninstall left $prefactor" >&2; exit 1; }
echo "Smoke test passed for $tag."
