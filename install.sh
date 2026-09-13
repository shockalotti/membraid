#!/bin/sh
# Install membraid from the latest GitHub release (Linux and macOS).
#
#   curl -fsSL https://github.com/shockalotti/membraid/releases/latest/download/install.sh | sh
#
# Options, as environment variables:
#   MEMBRAID_LITE=1         the lite build: smaller, no built-in embedding model (Ollama only)
#   MEMBRAID_VERSION=v0.4.0 a specific release instead of the latest
#   MEMBRAID_DIR=~/bin      where to put the binary (default ~/.local/bin)
#
# The download is checked against the release's checksums.txt before anything
# is installed. Later updates: membraid update.
set -eu

repo="shockalotti/membraid"
dir="${MEMBRAID_DIR:-$HOME/.local/bin}"

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) echo "membraid: this script supports Linux and macOS; on Windows download membraid-windows-*.exe from https://github.com/$repo/releases" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) echo "membraid: no build for $(uname -m)" >&2; exit 1 ;;
esac

name="membraid-$os-$arch"
[ "${MEMBRAID_LITE:-}" = 1 ] && name="$name-lite"
if [ -n "${MEMBRAID_VERSION:-}" ]; then
  base="https://github.com/$repo/releases/download/$MEMBRAID_VERSION"
else
  base="https://github.com/$repo/releases/latest/download"
fi

fetch() {
  if command -v curl >/dev/null 2>&1; then curl -fsSL "$1" -o "$2"
  elif command -v wget >/dev/null 2>&1; then wget -q "$1" -O "$2"
  else echo "membraid: needs curl or wget" >&2; exit 1
  fi
}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
echo "downloading $name..."
fetch "$base/$name" "$tmp/$name"
fetch "$base/checksums.txt" "$tmp/checksums.txt"

want=$(awk -v n="$name" '$2 == n {print $1}' "$tmp/checksums.txt")
if command -v sha256sum >/dev/null 2>&1; then got=$(sha256sum "$tmp/$name" | awk '{print $1}')
else got=$(shasum -a 256 "$tmp/$name" | awk '{print $1}')
fi
if [ -z "$want" ] || [ "$got" != "$want" ]; then
  echo "membraid: $name does not match checksums.txt; nothing was installed" >&2
  exit 1
fi

mkdir -p "$dir"
chmod 755 "$tmp/$name"
mv "$tmp/$name" "$dir/membraid"
"$dir/membraid" version

case ":$PATH:" in
  *":$dir:"*) ;;
  *) echo "note: $dir is not on your PATH; add it, or run $dir/membraid" ;;
esac
echo "next: membraid init (or clone your vault), then membraid install"
