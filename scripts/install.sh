#!/bin/sh
set -eu

# Usage: sh install.sh [vX.Y.Z] [installation-directory]
# DDB_RELEASE_BASE_URL and DDB_LATEST_API support mirrors and fixture tests.
version=${1:-latest}
install_dir=${2:-${DDB_INSTALL_DIR:-"$HOME/.local/bin"}}
release_base=${DDB_RELEASE_BASE_URL:-https://github.com/paveforge/go-ddb-cli/releases/download}
latest_api=${DDB_LATEST_API:-https://api.github.com/repos/paveforge/go-ddb-cli/releases/latest}
fail() { printf '%s\n' "Error: $*" >&2; exit 1; }
fetch() {
  if command -v curl >/dev/null 2>&1; then curl -fLsS "$1" -o "$2"
  elif command -v wget >/dev/null 2>&1; then wget -qO "$2" "$1"
  else fail 'curl or wget is required'; fi
}
case "$(uname -s)" in Linux) os=linux;; Darwin) os=darwin;; *) fail 'unsupported OS; use install.ps1 on Windows';; esac
case "$(uname -m)" in x86_64|amd64) arch=amd64;; arm64|aarch64) arch=arm64;; *) fail 'unsupported architecture';; esac
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
if [ "$version" = latest ]; then
  fetch "$latest_api" "$work_dir/release.json"
  version=$(sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$work_dir/release.json" | head -n 1)
fi
case "$version" in v*) ;; *) version="v$version";; esac
printf '%s\n' "$version" | LC_ALL=C grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$' || fail 'invalid version (expected vX.Y.Z)'
asset="ddb_${version#v}_${os}_${arch}.tar.gz"
fetch "$release_base/$version/$asset" "$work_dir/$asset"
fetch "$release_base/$version/checksums.txt" "$work_dir/checksums.txt"
expected=$(awk -v name="$asset" '$2 == name { print $1 }' "$work_dir/checksums.txt")
printf '%s\n' "$expected" | grep -Eq '^[a-fA-F0-9]{64}$' || fail 'missing or ambiguous checksum'
if command -v sha256sum >/dev/null 2>&1; then actual=$(sha256sum "$work_dir/$asset" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then actual=$(shasum -a 256 "$work_dir/$asset" | awk '{print $1}')
else fail 'sha256sum or shasum is required'; fi
[ "$expected" = "$actual" ] || fail 'checksum mismatch'
tar -xzf "$work_dir/$asset" -C "$work_dir" ddb
[ -f "$work_dir/ddb" ] && [ ! -L "$work_dir/ddb" ] || fail 'archive has no regular ddb binary'
mkdir -p "$install_dir"
# Stage beside the destination so replacement stays on the same filesystem.
staged=$(mktemp "$install_dir/.ddb-install.XXXXXX")
trap 'rm -rf "$work_dir"; [ -z "${staged:-}" ] || rm -f "$staged"' EXIT
cp "$work_dir/ddb" "$staged"
chmod 755 "$staged"
mv -f "$staged" "$install_dir/ddb"
staged=
printf 'Installed ddb %s to %s/ddb\n' "$version" "$install_dir"
case ":$PATH:" in *":$install_dir:"*) ;; *) printf 'Add %s to your PATH.\n' "$install_dir";; esac
