#!/usr/bin/env bash
# Installs the gidm Chrome native-messaging host manifest so the extension can
# reach the daemon. Run this AFTER loading the unpacked extension — its ID only
# exists once Chrome has loaded it.
#
#   ./scripts/install-chrome-host.sh <EXTENSION_ID>
#
# Find <EXTENSION_ID> at chrome://extensions (enable Developer mode).
set -euo pipefail

ID="${1:-}"
if [ -z "$ID" ]; then
  echo "usage: $0 <EXTENSION_ID>   (from chrome://extensions, Developer mode)" >&2
  exit 1
fi

REPO="$(cd "$(dirname "$0")/.." && pwd)"
HOST_BIN="$REPO/bin/gidm-host"
if [ ! -x "$HOST_BIN" ]; then
  echo "gidm-host not built: run 'make build' first (expected $HOST_BIN)" >&2
  exit 1
fi

NAME="io.github.k_red90.gidm"
# macOS, Google Chrome. For other Chromium browsers, swap the directory:
#   Chromium: ~/Library/Application Support/Chromium/NativeMessagingHosts
#   Brave:    ~/Library/Application Support/BraveSoftware/Brave-Browser/NativeMessagingHosts
#   Edge:     ~/Library/Application Support/Microsoft Edge/NativeMessagingHosts
DEST="$HOME/Library/Application Support/Google/Chrome/NativeMessagingHosts"
mkdir -p "$DEST"

cat > "$DEST/$NAME.json" <<EOF
{
  "name": "$NAME",
  "description": "gidm native messaging host",
  "path": "$HOST_BIN",
  "type": "stdio",
  "allowed_origins": [
    "chrome-extension://$ID/"
  ]
}
EOF

echo "Installed $DEST/$NAME.json"
echo "  host:      $HOST_BIN"
echo "  extension: chrome-extension://$ID/"
echo "Reload the extension (toggle off/on at chrome://extensions) so Chrome re-reads the manifest."
