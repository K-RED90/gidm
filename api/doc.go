// Package api is gidm's shared, stdlib-only wire protocol: the versioned
// request/response contract imported by both the daemon (gidmd) and the CLI
// (gidm). It is a leaf — it imports only the standard library and never the
// engine, store, httpx, httpfetch, or any Manager code, so a client linking
// this package never transitively pulls in the engine. Everything here is pure
// data: there is no network I/O, no package-level mutable state, and no init.
//
// # Wire format
//
// Framing is newline-delimited JSON (NDJSON): exactly one JSON object per line,
// the request line followed by the response line. NDJSON is the simplest format
// to implement and test with encoding/json's streaming Encoder/Decoder, it is
// self-framing without a length prefix, and one object maps cleanly to one
// message. WriteMessage and ReadMessage (frame.go) are the single shared
// implementation of this framing so the daemon and CLI cannot drift.
//
// # Version invariant
//
// Version is the protocol version. It is bumped only on an incompatible change.
// A peer stamps Version on every Request and Response; when the server receives
// a Version it cannot serve it answers with an error Response carrying
// CodeUnsupportedVersion.
//
// # Error-code invariant
//
// Response error codes (response.go) are a closed, stable, machine-readable
// enum. They never expose internal/engine identifiers or internal error text:
// the Code is for programmatic handling and the Message is for humans. Adding a
// code is a protocol change; renaming or removing one is breaking.
package api
