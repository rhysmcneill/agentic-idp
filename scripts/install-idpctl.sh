#!/usr/bin/env sh
# Installs idpctl from a GitHub Release, verifying its SHA-256 checksum.
#   IDPCTL_VERSION  version to install, e.g. 0.3.0 (default: latest idpctl release)
#   IDPCTL_BIN_DIR  install directory (default: /usr/local/bin, else ~/.local/bin)
set -eu

repo="rhysmcneill/agentic-idp"

fail() { echo "error: $*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || fail "$1 is required"; }

need curl
need tar

case "$(uname -s)" in
  Linux)  os=linux ;;
  Darwin) os=darwin ;;
  *) fail "unsupported OS $(uname -s); on Windows download the .zip from the releases page" ;;
esac

case "$(uname -m)" in
  x86_64|amd64)  arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  armv7l|armv6l) arch=arm ;;
  *) fail "unsupported architecture $(uname -m)" ;;
esac

version="${IDPCTL_VERSION:-}"
if [ -z "$version" ]; then
  # The repo's "latest" release is whichever stream released last (platform, chart or
  # CLI), so find the newest idpctl-v* tag explicitly.
  version="$(curl -fsSL "https://api.github.com/repos/$repo/releases?per_page=100" \
    | grep -o '"tag_name": *"idpctl-v[0-9][^"]*"' \
    | head -n1 \
    | sed 's/.*idpctl-v//; s/"$//')"
  [ -n "$version" ] || fail "could not find an idpctl release"
fi

name="idpctl_${version}_${os}_${arch}"
base="https://github.com/$repo/releases/download/idpctl-v${version}"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "Downloading idpctl ${version} (${os}/${arch})"
curl -fsSL -o "$tmp/$name.tar.gz" "$base/$name.tar.gz" || fail "download failed: $base/$name.tar.gz"
curl -fsSL -o "$tmp/checksums.txt" "$base/idpctl_${version}_checksums.txt" || fail "checksum download failed"

expected="$(grep " $name.tar.gz\$" "$tmp/checksums.txt" | cut -d' ' -f1)"
[ -n "$expected" ] || fail "no checksum listed for $name.tar.gz"
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$tmp/$name.tar.gz" | cut -d' ' -f1)"
else
  actual="$(shasum -a 256 "$tmp/$name.tar.gz" | cut -d' ' -f1)"
fi
[ "$expected" = "$actual" ] || fail "checksum mismatch for $name.tar.gz"

tar -xzf "$tmp/$name.tar.gz" -C "$tmp"

dir="${IDPCTL_BIN_DIR:-}"
if [ -z "$dir" ]; then
  if [ -w /usr/local/bin ]; then dir=/usr/local/bin; else dir="$HOME/.local/bin"; fi
fi
mkdir -p "$dir"
install -m 0755 "$tmp/$name/idpctl" "$dir/idpctl"

echo "Installed idpctl ${version} to $dir/idpctl"
case ":$PATH:" in
  *":$dir:"*) ;;
  *) echo "note: $dir is not on your PATH" ;;
esac
