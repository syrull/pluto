#!/bin/sh
# Installs the latest pluto release binary for the current OS/arch.
set -eu

REPO="syrull/pluto"
BIN="pluto"
INSTALL_DIR="${PLUTO_INSTALL_DIR:-$HOME/.local/bin}"

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$arch" in
	x86_64 | amd64) arch="amd64" ;;
	arm64 | aarch64) arch="arm64" ;;
	*) echo "pluto: unsupported architecture: $arch" >&2; exit 1 ;;
esac
case "$os" in
	linux | darwin) ;;
	*) echo "pluto: unsupported OS: $os" >&2; exit 1 ;;
esac

asset="pluto_${os}_${arch}"
url="https://github.com/${REPO}/releases/latest/download/${asset}"

if command -v curl >/dev/null 2>&1; then
	download() { curl -fsSL "$1" -o "$2"; }
elif command -v wget >/dev/null 2>&1; then
	download() { wget -qO "$2" "$1"; }
else
	echo "pluto: need curl or wget to download" >&2
	exit 1
fi

echo "pluto: downloading ${asset}..."
mkdir -p "$INSTALL_DIR"
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
download "$url" "$tmp"
chmod 0755 "$tmp"
mv "$tmp" "$INSTALL_DIR/$BIN"

echo "pluto: installed to $INSTALL_DIR/$BIN"
case ":$PATH:" in
	*":$INSTALL_DIR:"*) ;;
	*) echo "pluto: add $INSTALL_DIR to your PATH, then run 'pluto'" >&2 ;;
esac
