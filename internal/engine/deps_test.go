package engine_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestEngineImportInvariant locks in the layering rule: the engine is a pure
// library and must never depend on the daemon's wire protocol, control server,
// HTTP adapter, store implementation, or any binary. It shells out to
// `go list -deps`, which needs the toolchain on PATH (present in CI); skipped in
// -short mode.
func TestEngineImportInvariant(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping go list dependency check in -short mode")
	}
	const pkg = "github.com/K-RED90/gidm/internal/engine"
	out, err := exec.Command("go", "list", "-deps", pkg).CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}
	forbidden := []string{
		"github.com/K-RED90/gidm/api",
		"github.com/K-RED90/gidm/internal/apiserver",
		"github.com/K-RED90/gidm/internal/httpx",
		"github.com/K-RED90/gidm/internal/httpfetch",
		"github.com/K-RED90/gidm/internal/store",
		"github.com/K-RED90/gidm/internal/store/sqlite",
	}
	for dep := range strings.FieldsSeq(string(out)) {
		if strings.HasPrefix(dep, "github.com/K-RED90/gidm/cmd/") {
			t.Errorf("internal/engine must not depend on a binary: %s", dep)
		}
		for _, bad := range forbidden {
			if dep == bad {
				t.Errorf("internal/engine must not depend on %s", bad)
			}
		}
	}
}
