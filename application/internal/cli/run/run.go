package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/neuron-runtime/neuron/application/client"
	"github.com/neuron-runtime/neuron/application/config"
	"github.com/neuron-runtime/neuron/application/connection"
	"github.com/neuron-runtime/neuron/application/internal/cli/bootstrap"
	"github.com/neuron-runtime/neuron/application/internal/cli/build"
	"github.com/neuron-runtime/neuron/application/internal/cli/command"
	"github.com/neuron-runtime/neuron/application/internal/cli/output"
	"github.com/neuron-runtime/neuron/application/internal/cli/utils"
	"github.com/neuron-runtime/neuron/application/project"
	"github.com/neuron-runtime/neuron/shared/types/core"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
	"github.com/spf13/cobra"
)

var (
	verbose  bool
	input    string
	detach   bool
	rebuild  bool
	jsonMode bool
)

// New constructs and configures the Cobra command for executing Neuron assemblies.
func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:   command.Run,
		Short: "Run a Neuron Assembly",
		Long:  "Run a Neuron Assembly using the internal N.O.R.E runtime execution engine.",
		RunE:  runCmdHandler,
	}

	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose output to display event payloads")
	cmd.Flags().StringVar(&input, "input", "", "JSON input payload for execution (e.g., '{\"key\":\"value\"}')")
	cmd.Flags().BoolVar(&detach, "detach", false, "Return execution handles immediately without streaming live events")
	cmd.Flags().BoolVar(&rebuild, "build", false, "Rebuild the project before running (project directory required)")
	cmd.Flags().BoolVar(&jsonMode, "json", false, "Stream execution events as NDJSON (one JSON object per line)")

	return cmd
}

// runCmdHandler addresses a registered assembly and triggers execution.
//
// With a target (`neuron run acme-api@1.0.0`) the command runs that assembly
// from anywhere and never consults the project. Without a target it resolves
// the key from the project's build record (.neuron/build.json): the project is
// rebuilt automatically when its authoring fingerprint has changed, or when
// --build forces it. A project that has never been built is an error naming
// `neuron build`.
func runCmdHandler(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	cfg, ok := config.FromContext(ctx)
	if !ok {
		return fmt.Errorf("configuration not loaded")
	}

	if verbose {
		fmt.Fprintln(cmd.ErrOrStderr(), "neuron: running in verbose mode")
	}

	var key protocol.InstanceKey

	if len(args) == 0 {
		args = []string{""}
	}
	target, err := utils.NormalizeInstanceTarget(args[0])
	if err != nil {
		return err
	}

	if target == "" {
		// No target: the build record in the current project answers.
		key, err = ensureBuiltProject(cmd, cfg)
		if err != nil {
			return err
		}
	}

	c, cleanup, err := bootstrap.SetupClient(ctx, bootstrap.Options{
		Config:  cfg,
		Verbose: verbose,
	})
	if err != nil {
		return err
	}
	defer cleanup()

	var execInput map[string]any
	if input != "" {
		if err := json.Unmarshal([]byte(input), &execInput); err != nil {
			return fmt.Errorf("invalid --input JSON: %w", err)
		}
	} else {
		execInput = map[string]any{}
	}

	// The run is always requested in detach mode so the CLI owns event
	// rendering; `--detach` simply skips streaming and prints the handles.
	execResult, err := c.ExecuteByKeyOrTarget(ctx, key, target, execInput, core.ExecutionModeDetach)
	if err != nil {
		return err
	}

	if !detach {
		renderer, err := output.New(output.Options{
			Out:      cmd.OutOrStdout(),
			Assembly: assemblyLabel(key.AssemblyID, target),
			Mode:     presentationMode(),
			Verbose:  verbose,
		})
		if err != nil {
			return err
		}
		if err := streamEventsAndWait(ctx, c, execResult.InstanceID, execResult.ExecutionID, renderer); err != nil {
			_ = renderer.Close()
			return err
		}
		return renderer.Close()
	}

	fmt.Printf("execution started\n")
	fmt.Printf("  execution_id: %s\n", execResult.ExecutionID)
	fmt.Printf("  instance_id:  %s\n", execResult.InstanceID)
	fmt.Printf("  status:       %s\n", execResult.Status)
	return nil
}

// presentationMode maps CLI flags to an output mode. JSON wins over verbose so
// machine output is never contaminated by diagnostics.
func presentationMode() output.Mode {
	switch {
	case jsonMode:
		return output.ModeJSON
	case verbose:
		return output.ModeVerbose
	default:
		return output.ModeAuto
	}
}

// assemblyLabel derives a human-readable label for the executed assembly from the
// resolved key, falling back to the addressing target.
func assemblyLabel(assemblyID, target string) string {
	label := assemblyID
	if label == "" {
		label = target
	}
	if strings.HasPrefix(label, "inst_") {
		return "instance " + label
	}
	if i := strings.IndexByte(label, '@'); i >= 0 {
		return label[:i]
	}
	return label
}

// ensureBuiltProject resolves the registered assembly key for the current
// project, rebuilding it when the authoring inputs changed since the last
// build (or when --build forces a rebuild). A missing build record is an error
// pointing at `neuron build`.
func ensureBuiltProject(cmd *cobra.Command, cfg config.Config) (protocol.InstanceKey, error) {
	root, err := os.Getwd()
	if err != nil {
		return protocol.InstanceKey{}, fmt.Errorf("get current directory: %w", err)
	}

	var record project.BuildRecord
	err = project.LoadBuildRecord(root, &record)

	switch {
	case errors.Is(err, project.ErrNotBuilt):
		if !rebuild {
			return protocol.InstanceKey{}, fmt.Errorf(
				"project has not been built; run `neuron build` first (or use `neuron run --build` from the project)",
			)
		}
	case err != nil:
		return protocol.InstanceKey{}, fmt.Errorf("load build record: %w", err)
	case !rebuild:
		// A build record exists; only rebuild when the authoring inputs changed
		// since it was recorded.
		inputs, ferr := utils.AuthoringInputs(filepath.Clean(root), cfg)
		if ferr == nil {
			fp, cerr := project.ComputeFingerprint(inputs)
			if cerr == nil && fp == record.Fingerprint {
				return record.Key, nil
			}
		}
	}

	fmt.Fprintf(cmd.ErrOrStderr(), "neuron: project changed since the last build; rebuilding\n")
	bopts := build.Options{Root: root}
	if force, _ := cmd.Flags().GetBool("force"); force {
		bopts.Force = true
	}
	if err := build.ExecuteWith(cmd, bopts); err != nil {
		return protocol.InstanceKey{}, fmt.Errorf("rebuild project: %w", err)
	}

	var fresh project.BuildRecord
	if err := project.LoadBuildRecord(root, &fresh); err != nil {
		return protocol.InstanceKey{}, fmt.Errorf("load build record after rebuild: %w", err)
	}
	return fresh.Key, nil
}

// streamEventsAndWait streams execution events in real time until the
// execution reaches a terminal state, feeding each event to the renderer. It
// prefers the WebSocket transport and falls back to Server-Sent Events for
// transports that cannot open a WebSocket session.
func streamEventsAndWait(ctx context.Context, c *client.Client, instanceID string, executionID core.ID, renderer output.Renderer) error {
	err := c.StreamExecutionEventsWS(ctx, instanceID, executionID, func(evt protocol.StreamEvent) error {
		if herr := renderer.Handle(ctx, evt); herr != nil {
			return herr
		}
		if output.IsTerminalEvent(evt) {
			return errExecutionTerminal
		}
		return nil
	})

	switch {
	case err == nil, errors.Is(err, errExecutionTerminal), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return nil
	case errors.Is(err, connection.ErrWebSocketUnavailable):
		return streamEventsAndWaitSSE(ctx, c, instanceID, executionID, renderer)
	default:
		return err
	}
}

// errExecutionTerminal is sent by the streaming callback when the execution
// reaches a terminal state and the stream can be closed.
var errExecutionTerminal = errors.New("execution reached terminal state")

// streamEventsAndWaitSSE is the legacy Server-Sent Events streaming path, kept
// as a fallback for transports without WebSocket support.
func streamEventsAndWaitSSE(ctx context.Context, c *client.Client, instanceID string, executionID core.ID, renderer output.Renderer) error {
	eventCh := make(chan protocol.StreamEvent, 64)
	errCh := make(chan error, 1)

	go func() {
		errCh <- c.StreamExecutionEvents(ctx, instanceID, executionID, func(evt protocol.StreamEvent) error {
			eventCh <- evt
			return nil
		})
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errCh:
			if err != nil && !strings.Contains(err.Error(), "context canceled") {
				return err
			}
			return nil
		case evt, ok := <-eventCh:
			if !ok {
				return nil
			}
			if herr := renderer.Handle(ctx, evt); herr != nil {
				return herr
			}
			if output.IsTerminalEvent(evt) {
				return nil
			}
		}
	}
}
