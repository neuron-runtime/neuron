package storage_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestRuntimePackagesDoNotImportConcreteBackends keeps the storage boundary
// honest. Generic runtime packages may depend on storage.Store; only the
// composition root may depend on a concrete backend.
func TestRuntimePackagesDoNotImportConcreteBackends(t *testing.T) {
	const module = "github.com/neuron-runtime/neuron"

	packages := []string{
		module + "/nore/internal/execution",
		module + "/nore/internal/event",
		module + "/nore/internal/instance",
		module + "/nore/internal/assembly",
		module + "/nore/internal/planner",
	}

	for _, pkg := range packages {
		pkg := pkg
		t.Run(strings.TrimPrefix(pkg, module+"/"), func(t *testing.T) {
			out, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", pkg).Output()
			if err != nil {
				t.Fatalf("go list -deps %s: %v", pkg, err)
			}
			for _, dep := range strings.Fields(string(out)) {
				if strings.HasPrefix(dep, module+"/nore/internal/storage/") {
					t.Errorf("%s depends on concrete storage backend %s", pkg, dep)
				}
			}
		})
	}
}
