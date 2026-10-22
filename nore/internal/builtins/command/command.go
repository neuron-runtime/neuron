// Package command implements the neuron:core:command capability runtime.
package command

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/builtins/input"
	"github.com/neuron-runtime/neuron/nore/internal/builtins/paths"
	"github.com/neuron-runtime/neuron/nore/internal/contracts"
	"github.com/neuron-runtime/neuron/shared/types/builtins"
)

// Command runs a local program and returns its exit code, standard output and
// standard error.
//
// It is the boundary between an assembly and the programs already installed on
// the machine. The program and its arguments are passed to the operating
// system separately, so an argument can never become a command of its own.
//
// The policy bounds the run: which programs it may run, where it may run
// them, what environment it inherits, how long it may take, and how much
// output is retained.
type Command struct {
	// Policy bounds what a run may do. A zero policy means the compiled-in
	// ceilings.
	Policy builtins.Policy
}

// inheritedVariables are the environment variables a program inherits from the
// runtime.
//
// The environment of the runtime process is not the environment of the
// program: a program that inherits everything inherits the credentials of
// whoever started the runtime. Only the variables a program needs to behave
// like a program are inherited, and everything else is dropped.
var inheritedVariables = []string{"PATH", "HOME", "LANG", "LC_ALL", "TZ", "TMPDIR"}

// terminateGrace is how long a terminated program is given to exit before the
// runtime stops waiting for it.
const terminateGrace = 5 * time.Second

// Execute runs the program named by the capability's input.
func (c Command) Execute(ctx context.Context, execution contracts.ExecutionContext) (map[string]any, error) {
	// The policy is resolved here rather than trusted, because a runtime may
	// also be constructed outside the registry. The resolution only narrows,
	// so a policy that has already been resolved is unchanged.
	policy, err := builtins.Effective(c.Policy)
	if err != nil {
		return nil, fmt.Errorf("neuron:core:command: %w", err)
	}

	values := input.New("neuron:core:command", execution.CapabilityConfigurations, execution.Params)
	logger := contracts.LoggerOrNop(execution.Logger)

	program, err := values.RequiredString("command")
	if err != nil {
		return nil, err
	}

	args, _, err := values.StringSlice("args")
	if err != nil {
		return nil, err
	}

	timeout, err := runTimeout(values, policy)
	if err != nil {
		return nil, err
	}

	failOnFailure, err := values.Bool("failOnNonZeroExit", true)
	if err != nil {
		return nil, err
	}

	format, err := values.Format("format", input.FormatText)
	if err != nil {
		return nil, err
	}

	limit, err := outputLimit(values, policy)
	if err != nil {
		return nil, err
	}

	environment, err := buildEnvironment(values)
	if err != nil {
		return nil, err
	}

	// The working directory is confined to the permitted roots, so a program
	// cannot be pointed at a directory the policy does not allow.
	roots := paths.NewRoots(policy.ProjectRoot, policy.FilesystemRoots)
	base := roots.Base()

	workingDirectory, err := resolveWorkingDirectory(values, roots, base)
	if err != nil {
		return nil, err
	}

	// The program is resolved against the program path the program itself
	// runs with, so the program that is checked is the program that runs.
	resolved, err := resolveProgram(program, environment["PATH"], workingDirectory, policy.AllowedExecutables)
	if err != nil {
		return nil, err
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, resolved, args...)
	cmd.Dir = workingDirectory
	cmd.Env = environmentSlice(environment)

	// The program runs in its own process group, and the whole group is
	// stopped when the run ends. A program that starts a child does not get
	// to keep it once the run it belongs to is over.
	runInItsOwnGroup(cmd)
	cmd.Cancel = func() error { return terminate(cmd) }
	cmd.WaitDelay = terminateGrace

	if stdin, present, err := values.String("stdin"); err != nil {
		return nil, err
	} else if present {
		cmd.Stdin = strings.NewReader(stdin)
	}

	// The output is bounded while it is produced, not after: a program that
	// writes without limit must not be able to exhaust the runtime's memory.
	stdout := &cappedBuffer{limit: limit}
	stderr := &cappedBuffer{limit: limit}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	logger.Debug(ctx, "neuron:core:command: running the program",
		contracts.F("program", resolved),
		contracts.F("working_directory", workingDirectory),
	)

	started := time.Now()
	runErr := cmd.Run()
	elapsed := time.Since(started)

	// A cancelled execution is not a program that finished. The program was
	// stopped because the work it belonged to is gone, so the cancellation is
	// reported rather than an exit code the program never produced.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}

	// The deadline is the runtime's own bound on the run, which is different
	// from a cancellation: the program was still wanted, it simply took too
	// long.
	timedOut := errors.Is(runCtx.Err(), context.DeadlineExceeded)

	exitCode := 0
	if runErr != nil {
		var exitError *exec.ExitError
		if !errors.As(runErr, &exitError) {
			// The program never ran: it could not be found, was not
			// executable, or could not be started. Reporting exit code zero
			// here would claim a success that never happened.
			return nil, fmt.Errorf("neuron:core:command: run %q: %w", program, runErr)
		}
		exitCode = exitError.ExitCode()
	}

	output, err := representOutput(stdout.String(), format)
	if err != nil {
		return nil, err
	}

	result := map[string]any{
		"stdout":      output,
		"stderr":      stderr.String(),
		"exit_code":   float64(exitCode), // JSON numbers are float64 by default in Go
		"success":     exitCode == 0 && !timedOut,
		"timed_out":   timedOut,
		"duration_ms": float64(elapsed.Milliseconds()),
	}
	// The truncation is reported, so a caller can tell a shortened stream from
	// a complete one.
	if stdout.Truncated() {
		result["stdout_truncated"] = true
	}
	if stderr.Truncated() {
		result["stderr_truncated"] = true
	}

	if result["success"] == false && failOnFailure {
		if timedOut {
			return nil, fmt.Errorf("neuron:core:command: %q was terminated by its deadline of %s", program, timeout)
		}
		return nil, fmt.Errorf("neuron:core:command: %q exited with code %d%s", program, exitCode, stderrSummary(stderr.String()))
	}

	return result, nil
}

// runTimeout resolves how long the program may run.
//
// A capability may ask for a shorter timeout than the policy allows, but not
// for a longer one: the policy is the ceiling, and a capability is part of the
// assembly's input.
func runTimeout(values input.Values, policy builtins.Policy) (time.Duration, error) {
	limit, err := policy.Timeout()
	if err != nil {
		return 0, fmt.Errorf("neuron:core:command: %w", err)
	}

	requested, err := values.Duration("timeout", limit)
	if err != nil {
		return 0, err
	}
	if requested <= 0 {
		return 0, fmt.Errorf("neuron:core:command: parameter \"timeout\" must be positive")
	}
	if requested > limit {
		return 0, fmt.Errorf("neuron:core:command: the requested timeout %s exceeds the limit of %s", requested, limit)
	}
	return requested, nil
}

// outputLimit resolves how much output is retained per stream.
//
// The capability may ask for less than the policy allows, never for more.
func outputLimit(values input.Values, policy builtins.Policy) (int64, error) {
	requested, err := values.Int("maxOutputBytes", 0)
	if err != nil {
		return 0, err
	}
	if requested < 0 {
		return 0, fmt.Errorf("neuron:core:command: parameter \"maxOutputBytes\" must not be negative, got %d", requested)
	}
	if requested == 0 {
		return policy.MaxCommandOutputBytes, nil
	}
	if int64(requested) > policy.MaxCommandOutputBytes {
		return 0, fmt.Errorf("neuron:core:command: the requested output limit of %d bytes exceeds the limit of %d bytes",
			requested, policy.MaxCommandOutputBytes)
	}
	return int64(requested), nil
}

// resolveWorkingDirectory resolves the directory the program runs in.
//
// A directory outside the permitted roots is refused: the program is bounded
// by the same reach as everything else the built-ins do.
func resolveWorkingDirectory(values input.Values, roots paths.Roots, base string) (string, error) {
	cwd, present, err := values.String("cwd")
	if err != nil {
		return "", err
	}
	if !present || strings.TrimSpace(cwd) == "" {
		return base, nil
	}

	resolved, err := roots.Resolve(base, cwd)
	if err != nil {
		return "", fmt.Errorf("neuron:core:command: %w", err)
	}
	return resolved, nil
}

// buildEnvironment builds the environment the program runs with.
//
// The inherited variables come first, and the variables the capability asked
// for override them.
func buildEnvironment(values input.Values) (map[string]string, error) {
	environment := make(map[string]string, len(inheritedVariables))

	for _, name := range inheritedVariables {
		if value, ok := os.LookupEnv(name); ok {
			environment[name] = value
		}
	}

	requested, _, err := values.StringMap("env")
	if err != nil {
		return nil, err
	}
	for name, value := range requested {
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("neuron:core:command: an environment variable name is empty")
		}
		environment[name] = value
	}

	return environment, nil
}

// environmentSlice renders the environment as the list the operating system
// expects, sorted so that a run is reproducible.
func environmentSlice(environment map[string]string) []string {
	names := make([]string, 0, len(environment))
	for name := range environment {
		names = append(names, name)
	}
	sort.Strings(names)

	rendered := make([]string, 0, len(names))
	for _, name := range names {
		rendered = append(rendered, name+"="+environment[name])
	}
	return rendered
}

// resolveProgram resolves the program to run and checks it against the
// allowlist.
//
// The check is made on the resolved path rather than on the name, so a name
// cannot be used to reach a program that is not permitted.
func resolveProgram(program, programPath, workingDirectory string, allowed []string) (string, error) {
	resolved, err := lookupProgram(program, programPath, workingDirectory)
	if err != nil {
		return "", fmt.Errorf("neuron:core:command: %w", err)
	}

	if len(allowed) == 0 {
		return resolved, nil
	}

	for _, entry := range allowed {
		permitted, err := lookupProgram(entry, programPath, workingDirectory)
		if err != nil {
			continue
		}
		if permitted == resolved {
			return resolved, nil
		}
	}

	return "", fmt.Errorf("neuron:core:command: the program %q is not permitted; the permitted programs are %s",
		resolved, strings.Join(allowed, ", "))
}

// lookupProgram resolves a program name against a program path.
//
// A name that contains a separator is a path, and is resolved against the
// working directory the program will run in. A name that does not is looked up
// in each directory of the program path, in order.
func lookupProgram(program, programPath, workingDirectory string) (string, error) {
	if strings.ContainsRune(program, filepath.Separator) {
		candidate := program
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(workingDirectory, candidate)
		}
		if !isExecutable(candidate) {
			return "", fmt.Errorf("the program %q is not an executable file", candidate)
		}
		return filepath.Clean(candidate), nil
	}

	for _, directory := range filepath.SplitList(programPath) {
		if directory == "" {
			directory = "."
		}
		candidate := filepath.Join(directory, program)
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(workingDirectory, candidate)
		}
		if isExecutable(candidate) {
			return filepath.Clean(candidate), nil
		}
	}

	return "", fmt.Errorf("the program %q was not found on the program path", program)
}

// isExecutable reports whether the path is a file that can be executed.
func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode().Perm()&0o111 != 0
}

// representOutput converts the program's standard output into the
// representation the caller asked for.
//
// The rule is the same as for the http built-in: a JSON output is returned as
// the value it represents, and an output that is not JSON is refused in the
// JSON format rather than handed over as a string the caller cannot bind.
func representOutput(text string, format input.Format) (any, error) {
	if strings.TrimSpace(text) == "" {
		if format == input.FormatText {
			return "", nil
		}
		return nil, nil
	}

	if format == input.FormatText {
		return text, nil
	}

	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		return nil, fmt.Errorf("neuron:core:command: the program's standard output is not JSON; use format \"text\" to receive it as text")
	}
	return value, nil
}

// stderrSummary returns the last meaningful line of the error output, for an
// error message that says why the program failed.
func stderrSummary(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if line == "" {
			continue
		}
		const limit = 200
		if len(line) > limit {
			line = line[:limit] + "..."
		}
		return ": " + line
	}
	return ""
}

// cappedBuffer is a writer that retains at most limit bytes and remembers how
// much was discarded.
//
// The bytes it retains are a prefix of the stream, so the output still starts
// where the program wrote it. Writing past the limit succeeds — the program is
// not the one being punished for exceeding a limit — but the excess is
// dropped.
type cappedBuffer struct {
	buf       bytes.Buffer
	limit     int64
	discarded int64
}

// Write retains the bytes that fit and discards the rest.
func (c *cappedBuffer) Write(p []byte) (int, error) {
	if remaining := c.limit - int64(c.buf.Len()); remaining > 0 {
		if int64(len(p)) <= remaining {
			c.buf.Write(p)
			return len(p), nil
		}
		c.buf.Write(p[:remaining])
		c.discarded += int64(len(p)) - remaining
		return len(p), nil
	}
	c.discarded += int64(len(p))
	return len(p), nil
}

// String returns the retained bytes.
func (c *cappedBuffer) String() string {
	return c.buf.String()
}

// Truncated reports whether any bytes were discarded.
func (c *cappedBuffer) Truncated() bool {
	return c.discarded > 0
}
