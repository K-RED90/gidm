package api

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Validation here is defense-in-depth shared by both peers. The server is the
// enforcement point; the engine validates independently as well.
var (
	ErrInvalidURL = errors.New("url must be an absolute http or https URL")
	ErrEmptyID    = errors.New("id must be non-empty")
)

// ValidateAdd requires a.URL to be an absolute http or https URL with a host.
// Empty, relative, and non-http(s) schemes are rejected — the same scheme
// allow-list the redirect policy enforces.
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
	return nil
}

// ValidateID rejects empty or whitespace-only download IDs.
func ValidateID(id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("api: validate id: %w", ErrEmptyID)
	}
	return nil
}
