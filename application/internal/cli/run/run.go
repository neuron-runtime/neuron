package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Muhammad-Jay/neuron/application/client"
	"github.com/Muhammad-Jay/neuron/application/config"
	"github.com/Muhammad-Jay/neuron/application/connection"
	"github.com/Muhammad-Jay/neuron/application/internal/cli/bootstrap"
	"github.com/Muhammad-Jay/neuron/application/internal/cli/build"
	"github.com/Muhammad-Jay/neuron/application/internal/cli/command"
	"github.com/Muhammad-Jay/neuron/application/internal/cli/utils"
	"github.com/Muhammad-Jay/neuron/application/project"
	"github.com/Muhammad-Jay/neuron/shared/types/core"
	"github.com/Muhammad-Jay/neuron/shared/types/protocol"
	"github.com/spf13/cobra"
)

const (
	colorReset     = "\033[0m"
	colorTimestamp = "\033[36m" // Cyan
	colorKey       = "\033[94m" // Light blue
)

var (
	verbose bool
	input   string
	detach  bool
	rebuild bool
)

// New constructs and configures the Cobra command for executing Neuron systems.
func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:   command.Run,
		Short: "Run a Neuron System",
		Long:  "Run a Neuron System using the internal N.O.R.E runtime execution engine.",
		RunE:  runCmdHandler,
	}

	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose output to display event payloads")
	cmd.Flags().StringVar(&input, "input", "", "JSON input payload for execution (e.g., '{\"key\":\"value\"}')")
	cmd.Flags().BoolVar(&detach, "detach", false, "Return execution handles immediately without streaming live events")
	cmd.Flags().BoolVar(&rebuild, "build", false, "Rebuild the project before running (project directory required)")

	return cmd
}

// runCmdHandler addresses a registered system and triggers execution.
//
// With a target (`neuron run acme-api@1.0.0`) the command runs that system
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
		fmt.Println("Running in verbose mode.")
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
	} else {
		// A target addresses the system directly; the key stays empty and the
		// colon-encoded target carries the addressing through to the API.
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

	// `neuron run` command run in detach mode by default to render event logs,
	//  and hide logs if `--detach`
	mode := core.ExecutionModeDetach

	execResult, err := c.ExecuteByKeyOrTarget(ctx, key, target, execInput, mode)
	if err != nil {
		return err
	}

	if !detach {
		summary := newRunSummary(key.SystemID, target)
		if err := streamEventsAndWait(ctx, c, execResult.InstanceID, execResult.ExecutionID, summary); err != nil {
			return err
		}
		renderSummary(summary)
		return nil
	}

	ts := formatTime(time.Now().UnixNano())
	fmt.Printf("%s[%s]%s execution started\n", colorTimestamp, ts, colorReset)

	printNode("  ", "execution_id", string(execResult.ExecutionID))
	printNode("  ", "instance_id", execResult.InstanceID)
	printNode("  ", "status", execResult.Status)

	return nil
}

// ensureBuiltProject resolves the registered system key for the current
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
	if rebuild, _ := cmd.Flags().GetBool("force"); rebuild {
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
// execution reaches a terminal state, feeding each event to the run summary.
// It prefers the WebSocket transport and falls back to Server-Sent Events for
// transports that cannot open a WebSocket session.
func streamEventsAndWait(ctx context.Context, c *client.Client, instanceID string, executionID core.ID, summary *runSummary) error {
	err := c.StreamExecutionEventsWS(ctx, instanceID, executionID, func(evt protocol.StreamEvent) error {
		printEvent(evt)
		summary.observe(evt)
		if isTerminalEvent(evt.Type) {
			return errExecutionTerminal
		}
		return nil
	})

	switch {
	case err == nil, errors.Is(err, errExecutionTerminal), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return nil
	case errors.Is(err, connection.ErrWebSocketUnavailable):
		return streamEventsAndWaitSSE(ctx, c, instanceID, executionID, summary)
	default:
		return err
	}
}

// errExecutionTerminal is sent by the streaming callback when the execution
// reaches a terminal state and the stream can be closed.
var errExecutionTerminal = errors.New("execution reached terminal state")

func isTerminalEvent(eventType string) bool {
	switch eventType {
	case "execution.completed", "execution.failed", "execution.cancelled":
		return true
	default:
		return false
	}
}

// streamEventsAndWaitSSE is the legacy Server-Sent Events streaming path, kept
// as a fallback for transports without WebSocket support.
func streamEventsAndWaitSSE(ctx context.Context, c *client.Client, instanceID string, executionID core.ID, summary *runSummary) error {
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
			printEvent(evt)
			summary.observe(evt)
			if isTerminalEvent(evt.Type) {
				return nil
			}
		}
	}
}

// printEvent handles formatting and output for live stream events.
func printEvent(evt protocol.StreamEvent) {
	ts := formatTime(evt.OccurredAt)

	svc := ""
	if evt.ServiceID != "" {
		svc = fmt.Sprintf(" [%s]", evt.ServiceID)
	}

	if evt.Type == "service.log" {
		level := "info"
		msg := ""

		if len(evt.Payload) > 0 {
			var payload map[string]any
			if err := json.Unmarshal(evt.Payload, &payload); err == nil {
				if l, ok := payload["Level"].(string); ok && l != "" {
					level = l
				}
				if m, ok := payload["Message"].(string); ok {
					msg = m
				}
			}
		}

		fmt.Printf("%s[%s]%s %s%s %s[%s]%s %s\n", colorTimestamp, ts, colorReset, evt.Type, svc, colorTimestamp, level, colorReset, msg)
		return
	}

	// Print standardized standard lifecycle event
	fmt.Printf("%s[%s]%s %s%s\n", colorTimestamp, ts, colorReset, evt.Type, svc)

	// Render non-log payloads only when verbose mode is enabled
	if !verbose || len(evt.Payload) == 0 {
		return
	}

	var payload map[string]any
	if err := json.Unmarshal(evt.Payload, &payload); err == nil && len(payload) > 0 {
		keys := make([]string, 0, len(payload))
		for k := range payload {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			printNode("  ", k, payload[k])
		}
	}
}

// printNode recursively renders key-value data structures into formatted tree branches.
func printNode(indent string, key string, val any) {
	switch v := val.(type) {
	case map[string]any:
		if len(v) == 0 {
			fmt.Printf("%s%s%s%s: {}\n", indent, colorKey, key, colorReset)
			return
		}
		fmt.Printf("%s%s%s%s:\n", indent, colorKey, key, colorReset)
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			printNode(indent+"  ", k, v[k])
		}
	case []any:
		if len(v) == 0 {
			fmt.Printf("%s%s%s%s: []\n", indent, colorKey, key, colorReset)
			return
		}
		fmt.Printf("%s%s%s%s:\n", indent, colorKey, key, colorReset)
		for i, item := range v {
			printNode(indent+"  ", fmt.Sprintf("[%d]", i), item)
		}
	case string:
		if strings.Contains(v, "\n") {
			fmt.Printf("%s%s%s%s: |\n", indent, colorKey, key, colorReset)
			lines := strings.Split(strings.TrimSpace(v), "\n")
			for _, line := range lines {
				if line == "" {
					fmt.Println(indent + "  ")
				} else {
					fmt.Printf("%s  %s\n", indent, line)
				}
			}
		} else {
			fmt.Printf("%s%s%s%s: %s\n", indent, colorKey, key, colorReset, v)
		}
	default:
		if v == nil {
			fmt.Printf("%s%s%s%s: null\n", indent, colorKey, key, colorReset)
		} else {
			fmt.Printf("%s%s%s%s: %v\n", indent, colorKey, key, colorReset, v)
		}
	}
}

// runSummary accumulates the lifecycle of a streamed execution so the final
// block of a run can render the system, terminal status, per-service results,
// and the resolved aggregate outputs without further API round-trips.
type runSummary struct {
	system       string
	terminal     string
	failure      string
	services     map[string]string
	serviceOrder []string
	outputs      map[string]map[string]any
	outputOrder  []string
}

func newRunSummary(systemID, target string) *runSummary {
	system := systemID
	if system == "" {
		system = target
	}
	if strings.HasPrefix(system, "inst_") {
		system = "instance " + system
	} else if i := strings.IndexByte(system, '@'); i >= 0 {
		system = system[:i]
	}
	return &runSummary{
		system:   system,
		services: make(map[string]string),
		outputs:  make(map[string]map[string]any),
	}
}

// observe folds a streamed event into the summary.
func (s *runSummary) observe(evt protocol.StreamEvent) {
	switch evt.Type {
	case "service.started":
		s.setService(string(evt.ServiceID), "running")
	case "service.completed":
		s.setService(string(evt.ServiceID), "completed")
	case "service.failed":
		s.setService(string(evt.ServiceID), "failed")
	case "execution.completed":
		s.terminal = "completed"
		if len(evt.Payload) > 0 {
			var payload struct {
				Outputs map[string]map[string]any
			}
			if err := json.Unmarshal(evt.Payload, &payload); err == nil {
				for id := range payload.Outputs {
					if _, seen := s.outputs[id]; !seen {
						s.outputOrder = append(s.outputOrder, id)
					}
				}
				s.outputs = payload.Outputs
			}
		}
	case "execution.failed":
		s.terminal = "failed"
		if len(evt.Payload) > 0 {
			var payload struct {
				Message string
			}
			if err := json.Unmarshal(evt.Payload, &payload); err == nil {
				s.failure = payload.Message
			}
		}
	case "execution.cancelled":
		s.terminal = "cancelled"
	}
}

func (s *runSummary) setService(id, status string) {
	if _, ok := s.services[id]; !ok {
		s.serviceOrder = append(s.serviceOrder, id)
	}
	s.services[id] = status
}

// renderSummary prints the final System/Execution/Result block after a run's
// live stream has closed.
func renderSummary(s *runSummary) {
	fmt.Println()
	fmt.Printf("%sSystem:%s %s\n", colorKey, colorReset, s.system)

	status := s.terminal
	if status == "" {
		status = "terminated"
	}
	fmt.Printf("%sStatus:%s %s\n", colorKey, colorReset, status)

	fmt.Println("\nExecution")
	if len(s.serviceOrder) == 0 {
		printNode("  ", "services", "none")
	}
	for _, id := range s.serviceOrder {
		fmt.Printf("  %-16s %s\n", id, s.services[id])
	}

	if s.terminal == "failed" && s.failure != "" {
		fmt.Println("\nResult")
		printNode("  ", "error", s.failure)
	}
	if len(s.outputs) > 0 {
		fmt.Println("\nResult")
		if len(s.outputs) == 1 {
			for _, out := range s.outputs {
				keys := make([]string, 0, len(out))
				for k := range out {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					printNode("  ", k, out[k])
				}
			}
		} else {
			for _, id := range s.outputOrder {
				printNode("  ", id, s.outputs[id])
			}
		}
	}
}

// formatTime converts a unix nanosecond timestamp into a readable localized format.
func formatTime(unixNano int64) string {
	if unixNano <= 0 {
		return "n/a"
	}
	return time.Unix(0, unixNano).Local().Format("15:04:05.000")
}

// ExecuteResponse represents the result of triggering a system execution.
type ExecuteResponse struct {
	ExecutionID core.ID   `json:"execution_id"`
	InstanceID  string    `json:"instance_id"`
	Status      string    `json:"status"`
	Time        time.Time `json:"time"`
}
