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
)

// Command runs a local program and returns its exit code, standard output and
// standard error.
//
// It is the boundary between an assembly and the programs already installed on
// the machine. The program and its arguments are passed to the operating
// system separately, so an argument can never become a command of its own.
type Command struct{}

// Execute runs the program named by the capability's input.
func (Command) Execute(ctx context.Context, execution contracts.ExecutionContext) (map[string]any, error) {
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

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

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

	return map[string]any{
		"stdout":    stdout.String(),
		"stderr":    stderr.String(),
		"exit_code": float64(exitCode), // JSON numbers are float64 by default in Go
		"success":   exitCode == 0,
	}, nil
}
