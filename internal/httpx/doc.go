// Package httpx is gidm's HTTP layer: a single pooled client offering ranged
// GETs, retry with backoff, a bounded and secure redirect policy, and a URL
// probe. It is a leaf adapter — it imports only the standard library and
// internal/config, never the engine or other clients.
package httpx
