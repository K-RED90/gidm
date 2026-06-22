package api

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
)

// Validation here is defense-in-depth shared by both peers. The server is the
// enforcement point; the engine validates independently as well.
var (
	ErrInvalidURL         = errors.New("url must be an absolute http or https URL")
	ErrEmptyID            = errors.New("id must be non-empty")
	ErrInvalidPriority    = errors.New("priority must be low, normal, or high")
	ErrInvalidDestination = errors.New("dir must be an absolute path and filename a single name")
	ErrInvalidSegments    = errors.New("segments must be between 0 and 64")
	ErrInvalidRate        = errors.New("rate must be a non-negative number of bytes per second")
	ErrInvalidCredentials = errors.New("credentials contain an invalid header or control character")
)

// Credential bounds. These cap an absurd or hostile auth payload at the wire
// boundary; they are generous enough never to reject a real bearer token or
// cookie. Header names and values, and every text field, are additionally
// rejected for CR/LF to foreclose header injection (a value smuggling its own
// header line into the request).
const (
	maxHeaderCount    = 32
	maxHeaderKeyLen   = 256
	maxHeaderValueLen = 8192
	maxCredFieldLen   = 8192
)

// restrictedHeaders are headers the engine sets itself (or that govern message
// framing/routing); a caller may not override them via custom headers. Basic
// auth flows through Username/Password and an explicit cookie through Cookie, so
// Authorization and Cookie are reserved here to avoid two sources of truth.
var restrictedHeaders = map[string]struct{}{
	"host":              {},
	"content-length":    {},
	"connection":        {},
	"transfer-encoding": {},
	"range":             {},
	"authorization":     {},
	"cookie":            {},
}

// maxAddSegments is a coarse upper bound on a per-download segment override at
// the wire boundary; the daemon clamps further to its configured MaxSegments.
// It mirrors the default MaxSegments so a request is never rejected for a value
// the engine would have accepted, while still bounding an absurd ask.
const maxAddSegments = 64

// ValidateAdd requires a.URL to be an absolute http or https URL with a host.
// Empty, relative, and non-http(s) schemes are rejected — the same scheme
// allow-list the redirect policy enforces. The optional priority is validated too.
func ValidateAdd(a Add) error {
	u, err := url.Parse(a.URL)
	if err != nil {
		return fmt.Errorf("api: validate add url %q: %w", a.URL, ErrInvalidURL)
	}
	if !u.IsAbs() || u.Host == "" {
		return fmt.Errorf("api: validate add url %q: %w", a.URL, ErrInvalidURL)
	}
	switch u.Scheme {
	case "http", "https":
	default:
		return fmt.Errorf("api: validate add url %q: %w", a.URL, ErrInvalidURL)
	}
	if err := validateDestination(a.Dir, a.Filename); err != nil {
		return err
	}
	if a.Segments < 0 || a.Segments > maxAddSegments {
		return fmt.Errorf("api: validate add segments %d: %w", a.Segments, ErrInvalidSegments)
	}
	if a.Auth != nil {
		if err := ValidateCredentials(*a.Auth); err != nil {
			return err
		}
	}
	return ValidatePriority(a.Priority)
}

// ValidateCredentials rejects an auth payload that is oversized or could inject a
// header: CR/LF in any field, an ill-formed or reserved header name, and counts
// or lengths past the bounds above. It is the wire-boundary check; httpx also
// only ever sets these through net/http, which canonicalizes names.
func ValidateCredentials(c Credentials) error {
	for _, f := range []struct{ name, val string }{
		{"username", c.Username}, {"password", c.Password},
		{"referer", c.Referer}, {"cookie", c.Cookie},
	} {
		if len(f.val) > maxCredFieldLen || strings.ContainsAny(f.val, "\r\n") {
			return fmt.Errorf("api: validate credentials %s: %w", f.name, ErrInvalidCredentials)
		}
	}
	if len(c.Headers) > maxHeaderCount {
		return fmt.Errorf("api: validate credentials: %d headers exceeds %d: %w", len(c.Headers), maxHeaderCount, ErrInvalidCredentials)
	}
	for k, v := range c.Headers {
		if k == "" || len(k) > maxHeaderKeyLen || len(v) > maxHeaderValueLen {
			return fmt.Errorf("api: validate credentials header %q: %w", k, ErrInvalidCredentials)
		}
		if !validHeaderName(k) || strings.ContainsAny(v, "\r\n") {
			return fmt.Errorf("api: validate credentials header %q: %w", k, ErrInvalidCredentials)
		}
		if _, reserved := restrictedHeaders[strings.ToLower(k)]; reserved {
			return fmt.Errorf("api: validate credentials header %q is reserved: %w", k, ErrInvalidCredentials)
		}
	}
	return nil
}

// validHeaderName reports whether s is a valid RFC 7230 header field-name token
// (visible ASCII, no separators or control characters), so a malformed name can
// never reach net/http.
func validHeaderName(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isTokenChar(s[i]) {
			return false
		}
	}
	return true
}

func isTokenChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	return strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0
}

// ValidateSetAuth requires a non-empty ID and valid credentials.
func ValidateSetAuth(sa SetAuth) error {
	if err := ValidateID(sa.ID); err != nil {
		return err
	}
	return ValidateCredentials(sa.Auth)
}

// validateDestination rejects a destination override that could escape the
// download directory. A non-empty Dir must be an absolute, clean path (no ".."
// segments); a non-empty Filename must be a single path element (no separators,
// not "." or ".."). Empty values are valid — they fall back to daemon defaults.
// The engine sanitizes again via safeBase as defense in depth.
func validateDestination(dir, filename string) error {
	if err := validateDir(dir); err != nil {
		return err
	}
	if filename != "" {
		if filename == "." || filename == ".." || strings.ContainsAny(filename, `/\`) {
			return fmt.Errorf("api: validate add filename %q: %w", filename, ErrInvalidDestination)
		}
	}
	return nil
}

// validateDir rejects a directory override that could escape the download root: a
// non-empty Dir must be an absolute, clean path with no ".." segment. Empty is
// valid for add (falls back to the daemon default); set-config rejects empty
// separately, since it changes the default explicitly.
func validateDir(dir string) error {
	if dir != "" {
		if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || containsDotDot(dir) {
			return fmt.Errorf("api: validate dir %q: %w", dir, ErrInvalidDestination)
		}
	}
	return nil
}

// containsDotDot reports whether path has a ".." element under either separator,
// catching traversal that survives an absolute, otherwise-clean path.
func containsDotDot(path string) bool {
	segs := strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' })
	return slices.Contains(segs, "..")
}

// ValidateID rejects empty or whitespace-only download IDs.
func ValidateID(id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("api: validate id: %w", ErrEmptyID)
	}
	return nil
}

// ValidatePriority accepts the empty string (which the daemon resolves to its
// configured default) or one of the three named levels.
func ValidatePriority(p Priority) error {
	switch p {
	case "", PriorityLow, PriorityNormal, PriorityHigh:
		return nil
	default:
		return fmt.Errorf("api: validate priority %q: %w", p, ErrInvalidPriority)
	}
}

// ValidateSetPriority requires a non-empty ID and an explicit, valid level —
// unlike add, set-priority has no sensible empty default.
func ValidateSetPriority(sp SetPriority) error {
	if err := ValidateID(sp.ID); err != nil {
		return err
	}
	if sp.Priority == "" {
		return fmt.Errorf("api: validate set-priority: %w", ErrInvalidPriority)
	}
	return ValidatePriority(sp.Priority)
}

// ValidateSetRate requires a non-empty ID and a non-negative rate (0 removes the
// per-download cap).
func ValidateSetRate(sr SetRate) error {
	if err := ValidateID(sr.ID); err != nil {
		return err
	}
	if sr.MaxRate < 0 {
		return fmt.Errorf("api: validate set-rate %d: %w", sr.MaxRate, ErrInvalidRate)
	}
	return nil
}

// ValidateSetConfig validates a partial settings update: only present (non-nil)
// fields are checked. A download dir must be absolute and clean (and non-empty —
// set-config changes the value explicitly); the default segment count must be in
// [1, maxAddSegments]; the default priority must be a named level; rates must be
// non-negative.
func ValidateSetConfig(sc SetConfig) error {
	if sc.DownloadDir != nil {
		if *sc.DownloadDir == "" {
			return fmt.Errorf("api: validate set-config dir: %w", ErrInvalidDestination)
		}
		if err := validateDir(*sc.DownloadDir); err != nil {
			return err
		}
	}
	if sc.SegmentsPerDownload != nil {
		if *sc.SegmentsPerDownload < 1 || *sc.SegmentsPerDownload > maxAddSegments {
			return fmt.Errorf("api: validate set-config segments %d: %w", *sc.SegmentsPerDownload, ErrInvalidSegments)
		}
	}
	if sc.DefaultPriority != nil {
		if *sc.DefaultPriority == "" {
			return fmt.Errorf("api: validate set-config priority: %w", ErrInvalidPriority)
		}
		if err := ValidatePriority(*sc.DefaultPriority); err != nil {
			return err
		}
	}
	if sc.MaxRate != nil && *sc.MaxRate < 0 {
		return fmt.Errorf("api: validate set-config max_rate %d: %w", *sc.MaxRate, ErrInvalidRate)
	}
	if sc.PerDownloadMaxRate != nil && *sc.PerDownloadMaxRate < 0 {
		return fmt.Errorf("api: validate set-config per_download_max_rate %d: %w", *sc.PerDownloadMaxRate, ErrInvalidRate)
	}
	return nil
}
