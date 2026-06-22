package api

import (
	"encoding/json"
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

func TestValidateAddPriority(t *testing.T) {
	// A valid URL with each priority level (and the empty default) is accepted; an
	// unknown level is rejected with ErrInvalidPriority.
	for _, p := range []Priority{"", PriorityLow, PriorityNormal, PriorityHigh} {
		if err := ValidateAdd(Add{URL: "https://example.com/f", Priority: p}); err != nil {
			t.Errorf("ValidateAdd(priority=%q) = %v, want nil", p, err)
		}
	}
	if err := ValidateAdd(Add{URL: "https://example.com/f", Priority: "urgent"}); !errors.Is(err, ErrInvalidPriority) {
		t.Errorf("ValidateAdd(priority=urgent) = %v, want ErrInvalidPriority", err)
	}
}

func TestValidateAddDestination(t *testing.T) {
	const url = "https://example.com/f"
	// A real temp dir is an OS-absolute, clean path on every platform, so the
	// valid-destination cases hold on Windows too — a "/srv/downloads" literal is
	// neither absolute nor clean there, which the validators correctly reject.
	validDir := t.TempDir()
	tests := []struct {
		name     string
		dir      string
		filename string
		wantErr  bool
	}{
		{"both empty", "", "", false},
		{"abs dir only", validDir, "", false},
		{"abs dir and plain filename", validDir, "movie.mkv", false},
		{"plain filename only", "", "movie.mkv", false},
		{"relative dir", "downloads", "", true},
		{"dir with dotdot", "/srv/../etc", "", true},
		{"unclean dir trailing slash", "/srv/downloads/", "", true},
		{"filename with separator", "/srv", "sub/movie.mkv", true},
		{"filename with backslash", "/srv", `sub\movie.mkv`, true},
		{"filename dot", "", ".", true},
		{"filename dotdot", "", "..", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAdd(Add{URL: url, Dir: tt.dir, Filename: tt.filename})
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidDestination) {
					t.Fatalf("err = %v, want ErrInvalidDestination", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
		})
	}
}

func TestValidateAddSegments(t *testing.T) {
	const url = "https://example.com/f"
	for _, n := range []int{0, 1, 8, maxAddSegments} {
		if err := ValidateAdd(Add{URL: url, Segments: n}); err != nil {
			t.Errorf("ValidateAdd(segments=%d) = %v, want nil", n, err)
		}
	}
	for _, n := range []int{-1, maxAddSegments + 1} {
		if err := ValidateAdd(Add{URL: url, Segments: n}); !errors.Is(err, ErrInvalidSegments) {
			t.Errorf("ValidateAdd(segments=%d) = %v, want ErrInvalidSegments", n, err)
		}
	}
}

func TestValidateSetPriority(t *testing.T) {
	tests := []struct {
		name    string
		sp      SetPriority
		wantErr error
	}{
		{"ok high", SetPriority{ID: "d1", Priority: PriorityHigh}, nil},
		{"ok low", SetPriority{ID: "d1", Priority: PriorityLow}, nil},
		{"empty id", SetPriority{ID: "", Priority: PriorityHigh}, ErrEmptyID},
		{"empty priority", SetPriority{ID: "d1", Priority: ""}, ErrInvalidPriority},
		{"bad priority", SetPriority{ID: "d1", Priority: "urgent"}, ErrInvalidPriority},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSetPriority(tt.sp)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// TestAddDecodesWithoutPriority guards wire backward-compatibility: an add request
// JSON written before the priority field existed (no "priority" key) decodes to the
// empty Priority, which the daemon resolves to its configured default.
func TestAddDecodesWithoutPriority(t *testing.T) {
	const legacy = `{"version":1,"op":"add","add":{"url":"https://example.com/f"}}`
	var req Request
	if err := json.Unmarshal([]byte(legacy), &req); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if req.Add == nil {
		t.Fatal("add payload missing")
	}
	if req.Add.Priority != "" {
		t.Errorf("priority = %q, want empty (so the daemon applies its default)", req.Add.Priority)
	}
	// And a valid add with no priority passes validation.
	if err := ValidateAdd(*req.Add); err != nil {
		t.Errorf("ValidateAdd(legacy) = %v, want nil", err)
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
