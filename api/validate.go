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
)

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
	return ValidatePriority(a.Priority)
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
