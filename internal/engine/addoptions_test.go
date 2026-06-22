package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/K-RED90/gidm/internal/config"
)

func TestPlannedDest(t *testing.T) {
	e := engineWithDir("/downloads")
	const url = "https://example.com/files/archive.tar.gz"

	tests := []struct {
		name     string
		dir      string
		filename string
		want     string
	}{
		{"both empty defers to probe", "", "", ""},
		{"dir only keeps url basename", "/srv/dl", "", "/srv/dl/archive.tar.gz"},
		{"filename only uses default dir", "", "movie.mkv", "/downloads/movie.mkv"},
		{"dir and filename", "/srv/dl", "movie.mkv", "/srv/dl/movie.mkv"},
		{"hostile filename sanitized", "/srv/dl", "../../etc/passwd", "/srv/dl/passwd"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := e.plannedDest(tt.dir, tt.filename, url); got != tt.want {
				t.Errorf("plannedDest(%q,%q) = %q, want %q", tt.dir, tt.filename, got, tt.want)
			}
		})
	}
}

func TestClampSegments(t *testing.T) {
	e := &Engine{cfg: config.Download{MaxSegments: 8}}
	tests := []struct {
		in, want int
	}{
		{0, 0},  // 0 passes through: "use the configured default"
		{-3, 0}, // negative normalizes to the default sentinel
		{1, 1},  // in range
		{8, 8},  // at the ceiling
		{99, 8}, // clamped down to MaxSegments
	}
	for _, tt := range tests {
		if got := e.clampSegments(tt.in); got != tt.want {
			t.Errorf("clampSegments(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}

	// With no configured ceiling, a positive value passes through unchanged.
	noCeil := &Engine{cfg: config.Download{MaxSegments: 0}}
	if got := noCeil.clampSegments(100); got != 100 {
		t.Errorf("clampSegments(100) with no ceiling = %d, want 100", got)
	}
}

func TestSegmentsFor(t *testing.T) {
	e := &Engine{}
	e.defaultSegments.Store(4) // the default now lives in the runtime atomic, seeded by New
	if got := e.segmentsFor(&Download{}); got != 4 {
		t.Errorf("segmentsFor(default) = %d, want 4 (runtime default)", got)
	}
	if got := e.segmentsFor(&Download{SegmentCount: 12}); got != 12 {
		t.Errorf("segmentsFor(override) = %d, want 12", got)
	}
}

// TestSubmitAppliesOptions asserts Submit resolves and persists the destination
// and clamped segment count from AddOptions before the worker picks the job up.
func TestSubmitAppliesOptions(t *testing.T) {
	f := &fakeFetcher{content: makeContent(1 << 20), supportsRanges: true, sizeKnown: true}
	cfg := smallDownloadCfg()
	cfg.MaxSegments = 8
	m, store, _ := newManager(t, f, cfg, 1) // not started: the record stays queued

	id, err := m.Submit(context.Background(), "https://example.com/d.bin", PriorityNormal,
		AddOptions{Dir: "/srv/dl", Filename: "chosen.bin", Segments: 99})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	got, err := store.LoadDownload(context.Background(), id)
	if err != nil {
		t.Fatalf("LoadDownload: %v", err)
	}
	if want := filepath.Join("/srv/dl", "chosen.bin"); got.Destination != want {
		t.Errorf("Destination = %q, want %q", got.Destination, want)
	}
	if got.SegmentCount != 8 {
		t.Errorf("SegmentCount = %d, want 8 (clamped to MaxSegments)", got.SegmentCount)
	}

	// The bare-URL form persists no override so the engine derives both at run time.
	bareID, err := m.Submit(context.Background(), "https://example.com/bare.bin", PriorityNormal, AddOptions{})
	if err != nil {
		t.Fatalf("Submit (bare): %v", err)
	}
	bare, err := store.LoadDownload(context.Background(), bareID)
	if err != nil {
		t.Fatalf("LoadDownload (bare): %v", err)
	}
	if bare.Destination != "" || bare.SegmentCount != 0 {
		t.Errorf("bare add = {Destination:%q, SegmentCount:%d}, want both zero", bare.Destination, bare.SegmentCount)
	}
}

// TestRunHonorsCustomDestination drives a full transfer through the Manager with a
// destination override and asserts the file lands at the caller's dir/filename —
// creating a not-yet-existing directory — instead of the server-suggested name.
func TestRunHonorsCustomDestination(t *testing.T) {
	content := makeContent(2 << 20)
	f := &fakeFetcher{content: content, supportsRanges: true, sizeKnown: true, etag: `"v1"`, filename: "server-suggested.bin"}
	m, _, baseDir := newManager(t, f, smallDownloadCfg(), 2)

	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })

	customDir := filepath.Join(baseDir, "nested", "target") // does not exist yet
	id, err := m.Submit(context.Background(), "https://example.com/x.bin", PriorityNormal,
		AddOptions{Dir: customDir, Filename: "chosen.bin"})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitStatus(t, m, id, StatusCompleted, 5*time.Second)

	got, err := os.ReadFile(filepath.Join(customDir, "chosen.bin"))
	if err != nil {
		t.Fatalf("read custom dest: %v", err)
	}
	if sha256Hex(got) != sha256Hex(content) {
		t.Error("downloaded bytes do not match source")
	}
	// The server-suggested name must not have been used.
	if _, err := os.Stat(filepath.Join(customDir, "server-suggested.bin")); !os.IsNotExist(err) {
		t.Error("server-suggested filename was written despite an explicit override")
	}
}
