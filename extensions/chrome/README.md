# gidm Chrome extension (planned — milestone M4)

A Manifest V3 extension that intercepts downloads and hands them to the daemon
through the native-messaging host (`cmd/gidm-host`). `manifest.json` is a
skeleton; the Svelte UI and `background.js` service worker arrive in M4, sharing
components with the desktop app (`../../desktop`).

The host is registered per-OS using the manifest template in
`packaging/native-messaging/`; set `allowed_origins` to this extension's ID.
