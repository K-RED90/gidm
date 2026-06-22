// Package secret encrypts the small, sensitive blobs gidm must persist —
// per-download credentials (HTTP Basic password, cookies, custom headers) — so
// they are never written to the database in plaintext.
//
// A Vault seals and opens blobs with AES-256-GCM (authenticated encryption, so a
// tampered or foreign ciphertext fails to open rather than yielding junk). The
// 32-byte master key is held by the OS secret store — the macOS Keychain, the
// Linux Secret Service, or Windows DPAPI — and only falls back to a 0600 key file
// when no such store is reachable (a headless server, CI), so the key never sits
// decryptable beside the database. The package is pure Go and depends only on the
// standard library; the OS stores are reached through small os/exec or syscall
// shims, matching the project's std-lib-first, minimal-dependency rule.
package secret
