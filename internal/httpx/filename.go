package httpx

import "strings"

// SanitizeFilename reduces a server-suggested name to a bare, safe filename: it
// drops any directory components (either separator style), control bytes, and
// trailing dots/spaces, so the result can never escape a target directory nor
// alias a traversal sequence. It returns "" when nothing usable remains, leaving
// the caller to fall back to a name derived from the URL.
func SanitizeFilename(name string) string {
	// Collapse Windows separators, then keep only the final path element so any
	// "../" or "..\" traversal is discarded with the directory components.
	name = strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	name = stripControl(name)
	name = strings.TrimSpace(name)
	name = strings.TrimRight(name, " .") // ".."/"." collapse to ""; Windows-unsafe trailing dots/spaces go
	if name == "" {
		return ""
	}
	return name
}

// stripControl removes NUL and other control characters that have no place in a
// filename and could be used to smuggle separators past naive checks.
func stripControl(s string) string {
	return strings.Map(func(r rune) rune {
		if r == 0x7f || r < 0x20 {
			return -1
		}
		return r
	}, s)
}
