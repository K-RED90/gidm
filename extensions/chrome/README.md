# gidm Chrome extension

A Manifest V3 extension that captures browser downloads and hands them to the
gidm daemon, so downloads behind buttons/JS — and cookie- or referer-gated ones —
are accelerated by gidm instead of the browser. It forwards the resolved URL plus
the page's referrer and cookies through the native-messaging host
(`cmd/gidm-host`), which speaks the daemon's Unix-socket protocol.

## How it works

- `background.js` (service worker) listens for `chrome.downloads.onCreated`,
  gathers the URL + referrer + cookies, and hands them to the host. **Only on a
  confirmed hand-off does it cancel the browser's own download** — if the daemon
  is unreachable, the browser download proceeds normally and nothing is lost.
- A "Download with gidm" right-click menu captures any link directly.
- The popup has an on/off toggle and shows whether the daemon is reachable.

## Install (macOS, Chrome/Chromium)

1. Build the binaries (produces `./bin/gidm-host`):
   ```sh
   make build
   ```
2. Start the daemon (`./bin/gidmd`) and, optionally, the desktop app.
3. Load this folder unpacked: open `chrome://extensions`, enable **Developer
   mode**, click **Load unpacked**, and select `extensions/chrome/`.
4. Copy the extension's **ID** (shown on its card) and register the native host:
   ```sh
   ./scripts/install-chrome-host.sh <EXTENSION_ID>
   ```
5. Toggle the extension off/on so Chrome re-reads the host manifest.

Now click a download in the browser — it appears in gidm instead. The toolbar
icon flashes ✓ on capture, or `!` if gidm wasn't reachable (the browser then
keeps the download).

For Chromium/Brave/Edge, change the manifest directory in
`scripts/install-chrome-host.sh` (the alternatives are listed in its comments).

## Not yet (M4 second cut)

Media/HLS "Download this video" page detection, a Firefox port, a packaged
installer (Web Store listing + a signed/relocatable host path), and per-site
include/exclude rules.
