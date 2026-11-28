package typescript

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/neuron-runtime/neuron/application/process"
)

func TestTSLoaderPreservesBuildDiagnostics(t *testing.T) {
	const marker = "NEURON_TS_LOADER_DIAGNOSTIC_HELPER"
	if os.Getenv(marker) == "1" {
		fmt.Fprintln(os.Stderr, "TypeScript diagnostic: assembly result field \"orderId\" is missing")
		os.Exit(23)
	}

	cmd := process.Command{
		Path: os.Args[0],
		Args: []string{"-test.run=^TestTSLoaderPreservesBuildDiagnostics$"},
		Env:  append(os.Environ(), marker+"=1"),
	}
	err := NewTSLoader(cmd).BuildContext(context.Background())
	if err == nil {
		t.Fatal("BuildContext() succeeded; want subprocess failure")
	}
	if !strings.Contains(err.Error(), "exit status 23") {
		t.Errorf("error %q does not retain the process exit status", err)
	}
	if !strings.Contains(err.Error(), "TypeScript diagnostic: assembly result field") {
		t.Errorf("error %q does not include captured SDK output", err)
	}
}
