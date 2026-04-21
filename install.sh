#!/bin/sh
# repolink installer for macOS / Linux.
#
# Usage:
#   curl -sL https://raw.githubusercontent.com/khanakia/repolink-go/main/install.sh | sh
#
# Detects your OS + architecture, downloads the latest released binary
# from GitHub Releases, and installs it to /usr/local/bin.

set -e

REPO="khanakia/repolink-go"
BINARY="repolink"
INSTALL_DIR="/usr/local/bin"

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH" && exit 1 ;;
esac

case "$OS" in
  linux|darwin) ;;
  *) echo "Unsupported OS: $OS" && exit 1 ;;
esac

TAG=$(curl -sL "https://api.github.com/repos/${REPO}/releases/latest" \
  | grep -o '"tag_name": *"[^"]*"' \
  | head -1 \
  | cut -d'"' -f4)

if [ -z "$TAG" ]; then
  echo "Error: No release found at https://github.com/${REPO}/releases" && exit 1
fi

ASSET="${BINARY}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${TAG}/${ASSET}"

echo "Downloading ${BINARY} ${TAG} for ${OS}/${ARCH}..."
tmpdir=$(mktemp -d)
curl -sL "$URL" | tar xz -C "$tmpdir"

if [ ! -f "$tmpdir/$BINARY" ]; then
  echo "Error: ${BINARY} binary not found in archive." && exit 1
fi

echo "Installing to ${INSTALL_DIR}/${BINARY}..."
if [ -w "$INSTALL_DIR" ]; then
  mv "$tmpdir/$BINARY" "$INSTALL_DIR/$BINARY"
else
  sudo mv "$tmpdir/$BINARY" "$INSTALL_DIR/$BINARY"
fi
rm -rf "$tmpdir"

echo ""
echo "Installed. Next steps:"
echo "  cd <private-repo> && ${BINARY} setup         # register this clone"
echo "  cd <consumer-repo> && ${BINARY} link <src>   # add a mapping"
echo "  ${BINARY}                                     # sync current repo"
echo ""
echo "Verify: ${BINARY} version"
