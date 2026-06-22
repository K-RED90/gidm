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
	if dir != "" {
		if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || containsDotDot(dir) {
			return fmt.Errorf("api: validate add dir %q: %w", dir, ErrInvalidDestination)
		}
	}
	if filename != "" {
		if filename == "." || filename == ".." || strings.ContainsAny(filename, `/\`) {
			return fmt.Errorf("api: validate add filename %q: %w", filename, ErrInvalidDestination)
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
