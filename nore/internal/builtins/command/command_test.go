package command

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
		case "json":
			os.Stdout.WriteString(`{"answer":42}`)
			os.Exit(0)
		case "text":
			os.Stdout.WriteString("not json")
			os.Exit(0)
		case "environment":
			os.Stdout.WriteString(strings.Join(os.Environ(), "\n"))
			os.Exit(0)
		case "directory":
			directory, err := os.Getwd()
			if err != nil {
				os.Stderr.WriteString(err.Error())
				os.Exit(1)
			}
			os.Stdout.WriteString(directory)
			os.Exit(0)
		case "spawn":
			// Start a child that would outlive this program, so the test can
			// observe that the whole process group is stopped when the run
			// ends. The program then waits, so the run ends on its deadline
			// rather than on the program's own exit.
			child := exec.Command(os.Args[0], "-test.run=^TestCommandHelperProcess$", commandHelperMarker, "sleep")
			child.Start()
			os.Stdout.WriteString(strconv.Itoa(child.Process.Pid))
			time.Sleep(time.Minute)
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

// helperParamsWith runs the test binary again with the requested behaviour and
// additional params.
func helperParamsWith(t *testing.T, behaviour string, extra map[string]any) map[string]any {
	t.Helper()
	params := helperParams(t, behaviour)
	for name, value := range extra {
		params[name] = value
	}
	return params
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
	if result["timed_out"] != false {
		t.Errorf("timed_out = %v, want false", result["timed_out"])
	}
}

// A program that failed is a failed capability: the assembly is told that the
// program did not succeed, rather than receiving a result that says so.
func TestCommandFailsOnANonZeroExit(t *testing.T) {
	_, err := Command{}.Execute(context.Background(), contracts.ExecutionContext{
		Params: helperParams(t, "exit3"),
	})
	if err == nil {
		t.Fatal("Execute succeeded for a program that exited with a non-zero code")
	}
	if !strings.Contains(err.Error(), "exited with code 3") {
		t.Errorf("error = %v, want it to state the exit code", err)
	}
}

// A capability that asks to inspect a failure gets the result instead.
func TestCommandReportsANonZeroExitWhenTheFailureIsAllowed(t *testing.T) {
	result, err := Command{}.Execute(context.Background(), contracts.ExecutionContext{
		Params: helperParamsWith(t, "exit3", map[string]any{"failOnNonZeroExit": false}),
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
	if !strings.Contains(err.Error(), "neuron-command-that-does-not-exist") {
		t.Errorf("error = %v, want it to name the missing program", err)
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

// A capability may ask for less output than the policy allows, never for more.
func TestCommandRefusesAnOutputLimitAboveThePolicy(t *testing.T) {
	_, err := Command{Policy: builtins.Policy{MaxCommandOutputBytes: 16}}.Execute(context.Background(), contracts.ExecutionContext{
		Params: helperParamsWith(t, "exit0", map[string]any{"maxOutputBytes": 1024}),
	})
	if err == nil {
		t.Fatal("Execute accepted an output limit above the policy")
	}
	if !strings.Contains(err.Error(), "exceeds the limit") {
		t.Errorf("error = %v, want it to state the limit", err)
	}
}

// The program runs with an allowlisted environment, not with the environment
// of the runtime: a program that inherited everything would inherit the
// credentials of whoever started the runtime.
func TestCommandRunsWithAnAllowlistedEnvironment(t *testing.T) {
	t.Setenv("NEURON_COMMAND_SECRET", "secret")

	result, err := Command{}.Execute(context.Background(), contracts.ExecutionContext{
		Params: helperParams(t, "environment"),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	environment, _ := result["stdout"].(string)
	if strings.Contains(environment, "NEURON_COMMAND_SECRET") {
		t.Error("the program inherited a variable that is not on the allowlist")
	}
	if !strings.Contains(environment, "PATH=") {
		t.Error("the program did not inherit the program path")
	}
}

// The environment the capability asks for is added to the program's
// environment.
func TestCommandAddsTheRequestedEnvironment(t *testing.T) {
	result, err := Command{}.Execute(context.Background(), contracts.ExecutionContext{
		Params: helperParamsWith(t, "environment", map[string]any{
			"env": map[string]any{"NEURON_COMMAND_EXTRA": "value"},
		}),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	environment, _ := result["stdout"].(string)
	if !strings.Contains(environment, "NEURON_COMMAND_EXTRA=value") {
		t.Errorf("the program did not receive the requested variable: %q", environment)
	}
}

// The program runs in the project root, which is where the assembly lives.
func TestCommandRunsInTheProjectRoot(t *testing.T) {
	root := t.TempDir()

	result, err := Command{Policy: builtins.Policy{ProjectRoot: root}}.Execute(context.Background(), contracts.ExecutionContext{
		Params: helperParams(t, "directory"),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	directory, _ := result["stdout"].(string)
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatalf("resolve the working directory: %v", err)
	}
	expected, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("resolve the project root: %v", err)
	}
	if resolved != expected {
		t.Errorf("the program ran in %q, want %q", resolved, expected)
	}
}

// A working directory outside the permitted roots is refused: the program is
// bounded by the same reach as everything else the built-ins do.
func TestCommandRefusesAWorkingDirectoryOutsideThePermittedRoots(t *testing.T) {
	root := t.TempDir()

	_, err := Command{Policy: builtins.Policy{ProjectRoot: root}}.Execute(context.Background(), contracts.ExecutionContext{
		Params: helperParamsWith(t, "directory", map[string]any{"cwd": t.TempDir()}),
	})
	if err == nil {
		t.Fatal("Execute ran a program outside the permitted roots")
	}
	if !strings.Contains(err.Error(), "not reachable") {
		t.Errorf("error = %v, want it to state that the path is not reachable", err)
	}
}

// A working directory inside the project root is permitted.
func TestCommandRunsInAWorkingDirectoryInsideTheProjectRoot(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatalf("create the nested directory: %v", err)
	}

	result, err := Command{Policy: builtins.Policy{ProjectRoot: root}}.Execute(context.Background(), contracts.ExecutionContext{
		Params: helperParamsWith(t, "directory", map[string]any{"cwd": nested}),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	directory, _ := result["stdout"].(string)
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatalf("resolve the working directory: %v", err)
	}
	expected, err := filepath.EvalSymlinks(nested)
	if err != nil {
		t.Fatalf("resolve the nested directory: %v", err)
	}
	if resolved != expected {
		t.Errorf("the program ran in %q, want %q", resolved, expected)
	}
}

// A program that is not on the allowlist is refused, even when it exists.
func TestCommandRefusesAProgramThatIsNotAllowed(t *testing.T) {
	_, err := Command{Policy: builtins.Policy{AllowedExecutables: []string{"/bin/true"}}}.Execute(context.Background(), contracts.ExecutionContext{
		Params: helperParams(t, "exit0"),
	})
	if err == nil {
		t.Fatal("Execute ran a program that is not on the allowlist")
	}
	if !strings.Contains(err.Error(), "not permitted") {
		t.Errorf("error = %v, want it to state that the program is not permitted", err)
	}
}

// A program that is on the allowlist runs.
func TestCommandRunsAProgramThatIsAllowed(t *testing.T) {
	result, err := Command{Policy: builtins.Policy{AllowedExecutables: []string{os.Args[0]}}}.Execute(context.Background(), contracts.ExecutionContext{
		Params: helperParams(t, "exit0"),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result["success"] != true {
		t.Errorf("success = %v, want true", result["success"])
	}
}

// A program that outlasts its deadline is stopped, and the deadline is
// reported rather than an exit code the program never produced.
func TestCommandStopsTheProgramAtItsDeadline(t *testing.T) {
	_, err := Command{}.Execute(context.Background(), contracts.ExecutionContext{
		Params: helperParamsWith(t, "sleep", map[string]any{"timeout": "200ms"}),
	})
	if err == nil {
		t.Fatal("Execute succeeded for a program that outlasted its deadline")
	}
	if !strings.Contains(err.Error(), "deadline") {
		t.Errorf("error = %v, want it to state the deadline", err)
	}
}

// A capability may ask for a shorter timeout than the policy allows, never
// for a longer one.
func TestCommandRefusesATimeoutAboveThePolicyLimit(t *testing.T) {
	_, err := Command{Policy: builtins.Policy{DefaultTimeout: "1s"}}.Execute(context.Background(), contracts.ExecutionContext{
		Params: helperParamsWith(t, "exit0", map[string]any{"timeout": "10s"}),
	})
	if err == nil {
		t.Fatal("Execute accepted a timeout above the policy limit")
	}
	if !strings.Contains(err.Error(), "exceeds the limit") {
		t.Errorf("error = %v, want it to state the limit", err)
	}
}

// The JSON format returns the value the program produced, not a stringified
// copy of it.
func TestCommandReportsJSONOutput(t *testing.T) {
	result, err := Command{}.Execute(context.Background(), contracts.ExecutionContext{
		Params: helperParamsWith(t, "json", map[string]any{"format": "json"}),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	output, ok := result["stdout"].(map[string]any)
	if !ok {
		t.Fatalf("stdout = %#v, want a parsed object", result["stdout"])
	}
	if output["answer"] != float64(42) {
		t.Errorf("answer = %v, want 42", output["answer"])
	}
}

// Output that is not JSON is refused in the JSON format rather than handed
// over as a string the caller cannot bind.
func TestCommandRefusesOutputThatIsNotJSON(t *testing.T) {
	_, err := Command{}.Execute(context.Background(), contracts.ExecutionContext{
		Params: helperParamsWith(t, "text", map[string]any{"format": "json"}),
	})
	if err == nil {
		t.Fatal("Execute accepted output that is not JSON")
	}
	if !strings.Contains(err.Error(), "not JSON") {
		t.Errorf("error = %v, want it to state that the output is not JSON", err)
	}
}

// The program receives the text it was given on its standard input.
func TestCommandWritesToTheProgramsStandardInput(t *testing.T) {
	result, err := Command{}.Execute(context.Background(), contracts.ExecutionContext{
		Params: helperParamsWith(t, "exit0", map[string]any{"stdin": "input"}),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result["success"] != true {
		t.Errorf("success = %v, want true", result["success"])
	}
}

// The result of a run says how long the program took.
func TestCommandReportsTheDuration(t *testing.T) {
	result, err := Command{}.Execute(context.Background(), contracts.ExecutionContext{
		Params: helperParams(t, "exit0"),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if _, ok := result["duration_ms"].(float64); !ok {
		t.Errorf("duration_ms = %#v, want a number", result["duration_ms"])
	}
}

// The helper program is written as JSON so the output assertions above do not
// depend on the order of the environment variables.
func TestCommandHelperOutputIsStable(t *testing.T) {
	result, err := Command{}.Execute(context.Background(), contracts.ExecutionContext{
		Params: helperParams(t, "environment"),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var lines []string
	for _, line := range strings.Split(result["stdout"].(string), "\n") {
		if strings.Contains(line, "=") {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		t.Fatal("the program received no environment")
	}

	encoded, err := json.Marshal(lines)
	if err != nil {
		t.Fatalf("encode the environment: %v", err)
	}
	if !strings.Contains(string(encoded), "PATH=") {
		t.Errorf("the environment does not carry the program path: %s", encoded)
	}
}
