package progress

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Muhammad-Jay/neuron/application/executor"
)

func TestReporterLineOutput(t *testing.T) {
	// A non-terminal writer (bytes.Buffer) falls back to line-based output
	// with check marks and no color codes.
	var buf bytes.Buffer
	rep := New(&buf)

	rep.Resolving(executor.Requirement{Type: "example:echo", Version: "^1.0.0"})
	rep.Installing(executor.Package{Type: "example:echo", Version: "1.0.0", Registry: "local"})
	rep.Installed(executor.InstallResult{
		Installed: &executor.Installed{Type: "example:echo", Version: "1.0.0"},
	})

	rep.Stop()

	got := buf.String()
	want := "  · Resolving example:echo@^1.0.0 ...\n" +
		"  · Installing example:echo@1.0.0 (from local) ...\n" +
		"  ✓ example:echo@1.0.0 installed\n"

	if got != want {
		t.Fatalf("line output:\n%q\nwant:\n%q", got, want)
	}
	if strings.ContainsAny(got, "\x1b") {
		t.Fatalf("line output must never contain escape sequences: %q", got)
	}
}

func TestReporterAlreadyInstalled(t *testing.T) {
	var buf bytes.Buffer
	rep := New(&buf)

	rep.Resolving(executor.Requirement{Type: "example:echo"})
	rep.AlreadyInstalled(
		executor.Requirement{Type: "example:echo"},
		executor.Installed{Type: "example:echo", Version: "1.0.0"},
	)
	rep.Stop()

	got := buf.String()
	want := "  · Resolving example:echo ...\n" +
		"  ✓ example:echo@1.0.0 already installed\n"

	if got != want {
		t.Fatalf("line output:\n%q\nwant:\n%q", got, want)
	}
}
