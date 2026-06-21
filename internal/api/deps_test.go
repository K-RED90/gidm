package api

import (
	"os/exec"
	"strings"
	"testing"
)

// TestNoForbiddenImports locks in the leaf invariant: the protocol package must
// stay stdlib-only and must never transitively pull in the engine, store, or
// HTTP layers. It shells out to `go list -deps`, which needs the toolchain on
// PATH (present in CI); skipped in -short mode.
func TestNoForbiddenImports(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping go list dependency check in -short mode")
	}
	const pkg = "github.com/K-RED90/gidm/internal/api"
	out, err := exec.Command("go", "list", "-deps", pkg).CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}
	forbidden := []string{
		"github.com/K-RED90/gidm/internal/engine",
		"github.com/K-RED90/gidm/internal/store",
		"github.com/K-RED90/gidm/internal/httpx",
		"github.com/K-RED90/gidm/internal/httpfetch",
	}
	for dep := range strings.FieldsSeq(string(out)) {
		for _, bad := range forbidden {
			if dep == bad {
				t.Errorf("internal/api must not depend on %s", bad)
			}
		}
	}
}
