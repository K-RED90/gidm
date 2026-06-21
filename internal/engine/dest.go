package engine

import (
	"net/url"
	"path"
	"path/filepath"
	"strings"
)

// defaultBaseName is used when neither the server nor the URL yields a usable
// filename.
const defaultBaseName = "download"

// destPath derives the final on-disk destination inside the engine's download
// directory. The base name comes from the server's suggested filename (already
// sanitized by the HTTP layer) and falls back to the URL path's last element.
//
// The result is always filepath.Join(downloadDir, base) where base is reduced to
// a single path element via filepath.Base(filepath.Clean("/"+name)); this
// strips any "..", absolute prefixes, or separators, so a hostile
// Content-Disposition or URL cannot escape downloadDir (defense in depth on top
// of the HTTP layer's sanitizer).
func (e *Engine) destPath(probe ProbeInfo, rawURL string) string {
	name := probe.Filename
	if name == "" {
		name = baseFromURL(rawURL)
	}
	return filepath.Join(e.downloadDir, safeBase(name))
}

// safeBase collapses an arbitrary name to a single, separator-free filename that
// cannot traverse out of a directory.
func safeBase(name string) string {
	// filepath.Clean on "/"+name resolves ".." against the root and drops it;
	// Base then keeps only the final element.
	cleaned := filepath.Base(filepath.Clean("/" + name))
	if cleaned == "." || cleaned == string(filepath.Separator) || cleaned == "" {
		return defaultBaseName
	}
	return cleaned
}

// baseFromURL extracts the last element of a URL's path, decoding percent
// escapes. It uses path (slash-only) semantics because URL paths use "/"
// regardless of OS.
func baseFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return defaultBaseName
	}
	p := u.Path
	if p == "" {
		return defaultBaseName
	}
	base := path.Base(strings.TrimRight(p, "/"))
	if base == "." || base == "/" || base == "" {
		return defaultBaseName
	}
	return base
}
