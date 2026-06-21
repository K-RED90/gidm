package httpx

import "strings"

// SanitizeFilename reduces a server-suggested name to a bare, safe filename: it
// drops any directory components (either separator style), control bytes, and
// trailing dots/spaces, neutralizes characters Windows forbids, and escapes
// Windows reserved device names — so the result can never escape a target
// directory, alias a traversal sequence, or fail to create on Windows. The
// Windows rules are applied unconditionally so a file fetched on any OS stays
// portable. It returns "" when nothing usable remains, leaving the caller to
// fall back to a name derived from the URL.
func SanitizeFilename(name string) string {
	// Collapse Windows separators, then keep only the final path element so any
	// "../" or "..\" traversal is discarded with the directory components.
	name = strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	name = stripControl(name)
	name = replaceForbidden(name)
	name = strings.TrimSpace(name)
	name = strings.TrimRight(name, " .") // ".."/"." collapse to ""; Windows-unsafe trailing dots/spaces go
	if name == "" {
		return ""
	}
	if isReservedName(name) {
		name = "_" + name
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

// windowsForbidden lists the characters Windows disallows in filenames. Most
// other systems permit them, but we replace them so a downloaded file is safe to
// write and move across platforms.
const windowsForbidden = `<>:"|?*`

// replaceForbidden swaps each Windows-forbidden character for an underscore,
// preserving the rest of the name.
func replaceForbidden(s string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(windowsForbidden, r) {
			return '_'
		}
		return r
	}, s)
}

// reservedNames are Windows device names that stay reserved even with an
// extension ("CON.txt" still resolves to the console device).
var reservedNames = map[string]struct{}{
	"CON": {}, "PRN": {}, "AUX": {}, "NUL": {},
	"COM1": {}, "COM2": {}, "COM3": {}, "COM4": {}, "COM5": {},
	"COM6": {}, "COM7": {}, "COM8": {}, "COM9": {},
	"LPT1": {}, "LPT2": {}, "LPT3": {}, "LPT4": {}, "LPT5": {},
	"LPT6": {}, "LPT7": {}, "LPT8": {}, "LPT9": {},
}

// isReservedName reports whether name's stem (the part before its first dot)
// matches a Windows reserved device name, case-insensitively.
func isReservedName(name string) bool {
	stem := name
	if i := strings.IndexByte(stem, '.'); i >= 0 {
		stem = stem[:i]
	}
	_, ok := reservedNames[strings.ToUpper(stem)]
	return ok
}
