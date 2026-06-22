package main

import (
	"os/exec"
	"strings"
	"testing"
)

// TestNoForbiddenImports is the critical acceptance gate: the CLI must drive the
// daemon over the socket using the api package only and must never transitively pull
// in the engine, store, HTTP, or apiserver layers. It shells out to
// `go list -deps` WITHOUT the -test flag, so the external main_test package's
// heavy imports (which spin up a real gidmd) are excluded and do not trip it.
func TestNoForbiddenImports(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping go list dependency check in -short mode")
	}
	const pkg = "github.com/K-RED90/gidm/cmd/gidm"
	out, err := exec.Command("go", "list", "-deps", pkg).CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}
	forbidden := []string{
		"github.com/K-RED90/gidm/internal/engine",
		"github.com/K-RED90/gidm/internal/store",
		"github.com/K-RED90/gidm/internal/httpx",
		"github.com/K-RED90/gidm/internal/httpfetch",
		"github.com/K-RED90/gidm/internal/apiserver",
	}
	for dep := range strings.FieldsSeq(string(out)) {
		for _, bad := range forbidden {
			if dep == bad {
				t.Errorf("cmd/gidm must not depend on %s", bad)
			}
		}
	}
}
