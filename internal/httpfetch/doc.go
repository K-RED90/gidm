// Package httpfetch is the adapter binding internal/httpx to engine.Fetcher.
//
// It preserves the engine's pure-library decoupling: the engine imports only the
// standard library and internal/config, httpx imports only the standard library
// and internal/config, and neither imports the other. Only this adapter depends
// on both, mapping httpx's HTTP types to the engine's port.
package httpfetch
