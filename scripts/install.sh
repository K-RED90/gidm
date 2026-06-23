#!/bin/sh
# Installs the latest gidm CLI + daemon. No Go toolchain required.
#
#   curl -fsSL https://raw.githubusercontent.com/K-RED90/gidm/main/scripts/install.sh | sh
#
# Override the destination with GIDM_BIN_DIR (default /usr/local/bin), or pin a
# version with GIDM_VERSION=v0.1.0. macOS and Linux only — on Windows, download
# the .zip from the releases page.
set -eu

REPO="K-RED90/gidm"
BIN_DIR="${GIDM_BIN_DIR:-/usr/local/bin}"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  linux | darwin) ;;
  *) echo "gidm: unsupported OS '$os' — on Windows, grab the .zip from https://github.com/$REPO/releases/latest" >&2; exit 1 ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) echo "gidm: unsupported architecture '$arch'" >&2; exit 1 ;;
esac

# Resolve the release tag (e.g. v0.1.0): honour GIDM_VERSION, else ask GitHub for
# the latest. The asset name carries the version without the leading "v".
tag="${GIDM_VERSION:-}"
if [ -z "$tag" ]; then
  tag=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" |
    grep '"tag_name"' | head -1 | cut -d'"' -f4)
fi
[ -n "$tag" ] || { echo "gidm: could not determine the latest release" >&2; exit 1; }

asset="gidm_${tag#v}_${os}_${arch}.tar.gz"
url="https://github.com/$REPO/releases/download/$tag/$asset"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "Downloading $asset ($tag)..."
curl -fSL "$url" -o "$tmp/$asset"
tar -xzf "$tmp/$asset" -C "$tmp"

if mkdir -p "$BIN_DIR" 2>/dev/null && [ -w "$BIN_DIR" ]; then
  install -m 0755 "$tmp/gidm" "$tmp/gidmd" "$BIN_DIR/"
else
  echo "Installing to $BIN_DIR (needs sudo)..."
  sudo install -d "$BIN_DIR"
  sudo install -m 0755 "$tmp/gidm" "$tmp/gidmd" "$BIN_DIR/"
fi

echo "Installed gidm ${tag#v} -> $BIN_DIR/{gidm,gidmd}"
case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) echo "Note: $BIN_DIR is not on your PATH — add it to use 'gidm' directly." ;;
esac
