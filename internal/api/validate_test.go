package api

import (
	"errors"
	"testing"
)

func TestValidateAdd(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"http absolute", "http://example.com/f", false},
		{"https absolute", "https://example.com/f", false},
		{"https with port", "https://example.com:8443/f", false},
		{"empty", "", true},
		{"relative path", "/foo/bar", true},
		{"scheme-relative", "//example.com/f", true},
		{"ftp scheme", "ftp://example.com/f", true},
		{"file scheme", "file:///etc/passwd", true},
		{"no host", "http:///f", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAdd(Add{URL: tt.url})
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidURL) {
					t.Fatalf("err = %v, want ErrInvalidURL", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
		})
	}
}

func TestValidateID(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{"ok", "d1", false},
		{"empty", "", true},
		{"whitespace", "   ", true},
		{"tab", "\t", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateID(tt.id)
			if tt.wantErr {
				if !errors.Is(err, ErrEmptyID) {
					t.Fatalf("err = %v, want ErrEmptyID", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
		})
	}
}
