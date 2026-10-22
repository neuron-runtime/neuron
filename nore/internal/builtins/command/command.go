// Package command implements the neuron:core:command capability runtime.
package command

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"

	"github.com/neuron-runtime/neuron/nore/internal/builtins/input"
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
// The policy bounds the run: how long it may take and how much output is
// retained.
type Command struct {
	// Policy bounds what a run may do. A zero policy means the compiled-in
	// ceilings.
	Policy builtins.Policy
}

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

	program, err := values.RequiredString("command")
	if err != nil {
		return nil, err
	}

	args, _, err := values.StringSlice("args")
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, program, args...)

	// The output is bounded while it is produced, not after: a program that
	// writes without limit must not be able to exhaust the runtime's memory.
	stdout := &cappedBuffer{limit: policy.MaxCommandOutputBytes}
	stderr := &cappedBuffer{limit: policy.MaxCommandOutputBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	runErr := cmd.Run()

	// A cancelled execution is not a program that finished. exec.CommandContext
	// kills the process, so the error it reports is the kill signal rather
	// than the cancellation, and reporting that as an exit code would claim a
	// result the program never produced.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}

	exitCode := 0
	if runErr != nil {
		var exitError *exec.ExitError
		if !errors.As(runErr, &exitError) {
			// The program never ran: it could not be found, was not
			// executable, or could not be started. Reporting exit code zero
			// here would claim a success that never happened.
			return nil, fmt.Errorf("run %q: %w", program, runErr)
		}
		exitCode = exitError.ExitCode()
	}

	result := map[string]any{
		"stdout":    stdout.String(),
		"stderr":    stderr.String(),
		"exit_code": float64(exitCode), // JSON numbers are float64 by default in Go
		"success":   exitCode == 0,
	}
	// The truncation is reported, so a caller can tell a shortened stream from
	// a complete one.
	if stdout.Truncated() {
		result["stdout_truncated"] = true
	}
	if stderr.Truncated() {
		result["stderr_truncated"] = true
	}

	return result, nil
}

// cappedBuffer is a writer that retains at most limit bytes and remembers how
// much was discarded.
//
// The bytes it retains are a prefix of the stream, so the output still starts
// where the program wrote it. Writing past the limit succeeds — the program
// is not the one being punished for exceeding a limit — but the excess is
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
