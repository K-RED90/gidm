package engine

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDestPath(t *testing.T) {
	dir := "/downloads"
	e := &Engine{downloadDir: dir}

	tests := []struct {
		name     string
		probe    ProbeInfo
		url      string
		wantBase string
	}{
		{
			name:     "server filename preferred",
			probe:    ProbeInfo{Filename: "movie.mkv"},
			url:      "https://example.com/path/other.bin",
			wantBase: "movie.mkv",
		},
		{
			name:     "falls back to url basename",
			probe:    ProbeInfo{},
			url:      "https://example.com/files/archive.tar.gz",
			wantBase: "archive.tar.gz",
		},
		{
			name:     "percent-decoded url basename",
			probe:    ProbeInfo{},
			url:      "https://example.com/files/my%20file.pdf",
			wantBase: "my file.pdf",
		},
		{
			name:     "no name anywhere uses default",
			probe:    ProbeInfo{},
			url:      "https://example.com/",
			wantBase: defaultBaseName,
		},
		{
			name:     "url reserved device name escaped",
			probe:    ProbeInfo{},
			url:      "https://example.com/files/CON",
			wantBase: "_CON",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := e.destPath(tt.probe, tt.url)
			want := filepath.Join(dir, tt.wantBase)
			if got != want {
				t.Errorf("destPath = %q, want %q", got, want)
			}
		})
	}
}

// A hostile Content-Disposition or URL must never resolve outside downloadDir.
func TestDestPathContainsTraversal(t *testing.T) {
	dir := "/downloads"
	e := &Engine{downloadDir: dir}

	hostile := []ProbeInfo{
		{Filename: "../../etc/passwd"},
		{Filename: "../../../root/.ssh/authorized_keys"},
		{Filename: "/etc/shadow"},
		{Filename: "a/b/c.txt"},
	}
	for _, p := range hostile {
		got := e.destPath(p, "https://example.com/file")
		rel, err := filepath.Rel(dir, got)
		if err != nil {
			t.Fatalf("Rel(%q, %q): %v", dir, got, err)
		}
		if strings.HasPrefix(rel, "..") || strings.ContainsRune(rel, filepath.Separator) {
			t.Errorf("destPath(%q) = %q escaped or nested under %q (rel %q)", p.Filename, got, dir, rel)
		}
	}
}

func TestSafeBase(t *testing.T) {
	tests := map[string]string{
		"clean.txt":          "clean.txt",
		"../../etc/passwd":   "passwd",
		"/abs/path/file.bin": "file.bin",
		"a/b/c":              "c",
		"":                   defaultBaseName,
		"..":                 defaultBaseName,
		".":                  defaultBaseName,
		"/":                  defaultBaseName,
		`a<b>:c?.zip`:        "a_b__c_.zip",
		"CON":                "_CON",
		"nul.txt":            "_nul.txt",
	}
	for in, want := range tests {
		if got := safeBase(in); got != want {
			t.Errorf("safeBase(%q) = %q, want %q", in, got, want)
		}
	}
}
