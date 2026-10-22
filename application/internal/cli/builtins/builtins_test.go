package builtins

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// execute runs the command with the given arguments and returns its output.
func execute(t *testing.T, args ...string) (string, error) {
	t.Helper()

	cmd := New()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs(args)

	err := cmd.Execute()
	return out.String(), err
}

// The listing names every built-in with the type an assembly refers to it by.
func TestListNamesEveryBuiltin(t *testing.T) {
	out, err := execute(t)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	for _, name := range []string{"neuron:core:set", "neuron:core:http", "neuron:core:command", "neuron:core:filesystem"} {
		if !strings.Contains(out, name) {
			t.Errorf("the listing does not name %q:\n%s", name, out)
		}
	}
}

// The detail of a built-in states what it accepts and what it produces.
func TestDetailStatesTheContract(t *testing.T) {
	out, err := execute(t, "http")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	for _, expected := range []string{"neuron:core:http", "Parameters:", "url (string, required)", "Results:", "status_code (number, required)", "Constraints:"} {
		if !strings.Contains(out, expected) {
			t.Errorf("the detail does not state %q:\n%s", expected, out)
		}
	}
}

// The verbose detail explains what each parameter is for.
func TestVerboseDetailExplainsTheParameters(t *testing.T) {
	out, err := execute(t, "http", "--verbose")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !strings.Contains(out, "The absolute URL to request.") {
		t.Errorf("the verbose detail does not explain the parameters:\n%s", out)
	}
}

// A built-in that was removed is reported as removed, and the replacement is
// named.
func TestRemovedBuiltinIsReportedAsRemoved(t *testing.T) {
	_, err := execute(t, "delay")
	if err == nil {
		t.Fatal("Execute accepted a built-in that was removed")
	}
	if !strings.Contains(err.Error(), "was removed") {
		t.Errorf("error = %v, want it to report the removal", err)
	}
	if !strings.Contains(err.Error(), "instead") {
		t.Errorf("error = %v, want it to name a replacement", err)
	}
}

// A name that names no built-in is reported with the built-ins that exist.
func TestUnknownBuiltinIsReported(t *testing.T) {
	_, err := execute(t, "storage")
	if err == nil {
		t.Fatal("Execute accepted a name that names no built-in")
	}
	if !strings.Contains(err.Error(), "neuron:core:http") {
		t.Errorf("error = %v, want it to list the built-ins", err)
	}
}

// The canonical name of a built-in is accepted as well as the bare one.
func TestCanonicalNameIsAccepted(t *testing.T) {
	out, err := execute(t, "neuron:core:filesystem")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "neuron:core:filesystem") {
		t.Errorf("the detail does not name the built-in:\n%s", out)
	}
}

// The command is not bound to a project, so it reports the contract without a
// configuration.
func TestTheCommandNeedsNoProject(t *testing.T) {
	cmd := New()
	if cmd.RunE == nil {
		t.Fatal("the command has no handler")
	}
	var _ *cobra.Command = cmd
}
