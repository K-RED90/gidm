package main_test

import (
	"strings"
	"testing"
)

func TestConfigGetSetCLI(t *testing.T) {
	h := newHarness(t)

	// Default: an unlimited global cap.
	code, out, _ := h.runCLI("config", "get")
	if code != 0 || !strings.Contains(out, "Global speed cap") || !strings.Contains(out, "unlimited") {
		t.Fatalf("config get = %d %q", code, out)
	}

	// Set a global cap; the echo and a re-get must reflect it.
	code, out, errOut := h.runCLI("config", "set", "--max-rate=1048576")
	if code != 0 {
		t.Fatalf("config set = %d, stderr=%q", code, errOut)
	}
	if !strings.Contains(out, "1.0 MiB/s") {
		t.Errorf("config set echo = %q, want 1.0 MiB/s", out)
	}
	if _, out, _ := h.runCLI("config", "get"); !strings.Contains(out, "1.0 MiB/s") {
		t.Errorf("config get after set = %q", out)
	}

	// JSON form carries the raw value.
	if _, jsonOut, _ := h.runCLI("--json", "config", "get"); !strings.Contains(jsonOut, `"max_rate":1048576`) {
		t.Errorf("config get --json = %q", jsonOut)
	}
}

func TestConfigSetRejectsBadCLI(t *testing.T) {
	h := newHarness(t)
	if code, _, errOut := h.runCLI("config", "set", "--max-rate=-5"); code != 2 || !strings.Contains(errOut, "rate") {
		t.Fatalf("config set negative = %d, stderr=%q, want exit 2", code, errOut)
	}
	if code, _, _ := h.runCLI("config", "set"); code != 2 {
		t.Errorf("config set with no flags = %d, want 2", code)
	}
}

func TestSetRateCLI(t *testing.T) {
	h := newHarness(t)
	code, out, errOut := h.runCLI("add", h.fileURL)
	if code != 0 {
		t.Fatalf("add = %d, stderr=%q", code, errOut)
	}
	id := strings.TrimSpace(out)

	if code, out, _ := h.runCLI("set-rate", "--max-rate=524288", id); code != 0 || !strings.Contains(out, "rate set") {
		t.Fatalf("set-rate = %d %q", code, out)
	}
	if _, statusOut, _ := h.runCLI("status", id); !strings.Contains(statusOut, "512.0 KiB/s") {
		t.Errorf("status after set-rate = %q, want a 512.0 KiB/s limit", statusOut)
	}

	// Removing the cap clears it from status.
	if _, out, _ := h.runCLI("set-rate", "--max-rate=0", id); !strings.Contains(out, "rate set") {
		t.Errorf("set-rate 0 = %q", out)
	}
	if _, statusOut, _ := h.runCLI("status", id); strings.Contains(statusOut, "512.0 KiB/s") {
		t.Errorf("status still shows the removed cap: %q", statusOut)
	}
}
