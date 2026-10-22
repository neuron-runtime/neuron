package command

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
	"github.com/neuron-runtime/neuron/shared/types/builtins"
)

// commandHelperMarker is the argument that tells a re-executed test binary to
// act as the command built-in's helper program.
//
// The built-in passes the program and its arguments to the operating system
// separately, so the helper is selected by an argument rather than by an
// environment variable or by a program that happens to be installed.
const commandHelperMarker = "neuron-command-helper"

// TestCommandHelperProcess is the program the command built-in runs.
//
// It is not a test in its own right: it only does anything when the test
// binary has been re-executed with commandHelperMarker among its arguments.
func TestCommandHelperProcess(t *testing.T) {
	if !isCommandHelperProcess() {
		t.Skip("helper process: only meaningful when re-executed by the command built-in test")
	}

	for _, arg := range os.Args {
		switch arg {
		case "exit0":
			os.Exit(0)
		case "exit3":
			os.Exit(3)
		case "stderr":
			os.Stderr.WriteString("something went wrong")
			os.Exit(0)
		case "sleep":
			time.Sleep(time.Minute)
			os.Exit(0)
		case "flood":
			// Write far more than any output limit allows, so the test can
			// observe that the output is bounded while it is produced.
			for i := 0; i < 4096; i++ {
				os.Stdout.WriteString("0123456789")
			}
			os.Exit(0)
		}
	}
	os.Exit(0)
}

// isCommandHelperProcess reports whether this test binary was re-executed to
// act as the helper program.
func isCommandHelperProcess() bool {
	for _, arg := range os.Args {
		if arg == commandHelperMarker {
			return true
		}
	}
	return false
}

// helperParams runs the test binary again with the requested behaviour.
func helperParams(t *testing.T, behaviour string) map[string]any {
	t.Helper()
	return map[string]any{
		"command": os.Args[0],
		"args":    []any{"-test.run=^TestCommandHelperProcess$", commandHelperMarker, behaviour},
	}
}

func TestCommandRunsTheProgramAndReportsItsResult(t *testing.T) {
	result, err := Command{}.Execute(context.Background(), contracts.ExecutionContext{
		Params: helperParams(t, "exit0"),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result["success"] != true {
		t.Errorf("success = %v, want true", result["success"])
	}
	if result["exit_code"] != float64(0) {
		t.Errorf("exit_code = %v, want 0", result["exit_code"])
	}
}

func TestCommandReportsANonZeroExit(t *testing.T) {
	result, err := Command{}.Execute(context.Background(), contracts.ExecutionContext{
		Params: helperParams(t, "exit3"),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result["success"] != false {
		t.Errorf("success = %v, want false", result["success"])
	}
	if result["exit_code"] != float64(3) {
		t.Errorf("exit_code = %v, want 3", result["exit_code"])
	}
}

func TestCommandCapturesStandardError(t *testing.T) {
	result, err := Command{}.Execute(context.Background(), contracts.ExecutionContext{
		Params: helperParams(t, "stderr"),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got, _ := result["stderr"].(string); got != "something went wrong" {
		t.Errorf("stderr = %q, want %q", got, "something went wrong")
	}
}

// A program that never ran must not be reported as a program that succeeded.
func TestCommandFailsWhenTheProgramCannotBeStarted(t *testing.T) {
	_, err := Command{}.Execute(context.Background(), contracts.ExecutionContext{
		Params: map[string]any{"command": "neuron-command-that-does-not-exist"},
	})
	if err == nil {
		t.Fatal("Execute succeeded for a program that does not exist")
	}
	if !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("error %v does not report the missing program", err)
	}
}

// A cancelled execution is not a program that finished. Reporting an exit
// code here would claim a result the program never produced.
func TestCommandReportsCancellationRatherThanSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	_, err := Command{}.Execute(ctx, contracts.ExecutionContext{
		Params: helperParams(t, "sleep"),
	})
	if err == nil {
		t.Fatal("Execute succeeded for a cancelled command")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error %v does not report the cancellation", err)
	}
}

func TestCommandRequiresAProgram(t *testing.T) {
	_, err := Command{}.Execute(context.Background(), contracts.ExecutionContext{})
	if err == nil {
		t.Fatal("Execute succeeded without a command")
	}
}

func TestCommandHelperProcessIsSkippedInTheOrdinaryRun(t *testing.T) {
	if isCommandHelperProcess() {
		t.Skip("this is the helper process")
	}
	if strings.Contains(strings.Join(os.Args, " "), commandHelperMarker) {
		t.Errorf("the ordinary run carries the helper marker %q", commandHelperMarker)
	}
}

// Output beyond the limit is dropped while it is produced, and the truncation
// is reported so a caller can tell a shortened stream from a complete one.
func TestCommandBoundsTheOutputAndReportsTheTruncation(t *testing.T) {
	result, err := Command{Policy: builtins.Policy{MaxCommandOutputBytes: 16}}.Execute(context.Background(), contracts.ExecutionContext{
		Params: helperParams(t, "flood"),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	stdout, _ := result["stdout"].(string)
	if len(stdout) != 16 {
		t.Errorf("stdout is %d bytes, want 16", len(stdout))
	}
	if result["stdout_truncated"] != true {
		t.Errorf("stdout_truncated = %v, want true", result["stdout_truncated"])
	}
	if result["stderr_truncated"] == true {
		t.Errorf("stderr_truncated = %v, want no truncation on an empty stream", result["stderr_truncated"])
	}
}
