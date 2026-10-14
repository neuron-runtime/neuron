package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/neuron-runtime/neuron/application/client"
	"github.com/neuron-runtime/neuron/application/config"
	"github.com/neuron-runtime/neuron/application/connection"
	"github.com/neuron-runtime/neuron/application/internal/cli/command"
	"github.com/neuron-runtime/neuron/application/internal/cli/utils"
	"github.com/neuron-runtime/neuron/application/project"
	"github.com/neuron-runtime/neuron/shared/types/projectruntime"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
	"github.com/spf13/cobra"
)

var (
	params       string
	paramsFile   string
	overridePort string
	format       string
)

// New constructs and configures the Cobra command for executing Neuron assemblies.
func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:   command.Run,
		Short: "Run a Neuron Assembly",
		Long:  "Run a Neuron Assembly using the project runtime. Discovers the local runtime or connects to a remote endpoint.",
		RunE:  runCmdHandler,
	}

	cmd.Flags().StringVar(&params, "params", "", "JSON params payload for execution (e.g., '{\"key\":\"value\"}')")
	cmd.Flags().StringVar(&paramsFile, "params-file", "", "Path to JSON file with params payload")
	cmd.Flags().StringVar(&overridePort, "port", "", "Override local runtime port (default: auto-discover from project)")
	cmd.Flags().StringVar(&format, "format", "json", "Output format for Assembly result (json, text)")

	return cmd
}

// runCmdHandler runs an Assembly using the project runtime.
// It discovers the local runtime or connects to a remote endpoint.
func runCmdHandler(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	cfg, ok := config.FromContext(ctx)
	if !ok {
		return fmt.Errorf("configuration not loaded")
	}

	// The remote endpoint is the global --remote flag, read directly so run does
	// not shadow the root flag with a second identical definition.
	remoteURL, _ := cmd.Flags().GetString("remote")

	// Resolve target assembly if provided
	target := ""
	if len(args) > 0 {
		t, err := utils.NormalizeInstanceTarget(args[0])
		if err != nil {
			return err
		}
		target = t
	}

	// Load params from --params or --params-file
	var execParams map[string]any
	if params != "" && paramsFile != "" {
		return fmt.Errorf("--params and --params-file are mutually exclusive")
	}
	if paramsFile != "" {
		data, err := os.ReadFile(paramsFile)
		if err != nil {
			return fmt.Errorf("read params file: %w", err)
		}
		if err := json.Unmarshal(data, &execParams); err != nil {
			return fmt.Errorf("invalid params file JSON: %w", err)
		}
	} else if params != "" {
		if err := json.Unmarshal([]byte(params), &execParams); err != nil {
			return fmt.Errorf("invalid --params JSON: %w", err)
		}
	} else {
		execParams = map[string]any{}
	}

	// Resolve the runtime endpoint. A remote endpoint is explicit; otherwise
	// the local project runtime is discovered once and its record supplies both
	// the endpoint and the API token.
	var endpoint, token string
	if remoteURL != "" {
		endpoint = remoteURL
	} else {
		discovered, discoveredToken, err := discoverLocalRuntime(cfg, overridePort)
		if err != nil {
			return fmt.Errorf("discover local runtime: %w", err)
		}
		if discovered == "" {
			return fmt.Errorf("no project runtime found; start one with `neuron` in the project directory")
		}
		endpoint, token = discovered, discoveredToken
	}

	conn := connection.NewRemote(endpoint)
	c := client.New(conn)

	if token != "" {
		if transport, ok := conn.Transport().(*connection.HTTPTransport); ok {
			transport.WithToken(token)
		}
	}

	// Address the Assembly to run. The facade takes one selector query
	// parameter, so an explicit target is used verbatim and the current project
	// build's key is used otherwise; the server resolves it to a registered
	// Assembly.
	selector := target
	if selector == "" {
		key, err := resolveCurrentBuildKey(ctx, cfg)
		if err != nil {
			return err
		}
		selector = key.ColonString()
	}

	// Run synchronously through the facade to get the Assembly result.
	result, err := c.Run(ctx, selector, execParams)
	if err != nil {
		return err
	}

	// Output the Assembly result
	return outputResult(cmd, result, format)
}

// discoverLocalRuntime resolves the local project runtime for the project that
// owns the current directory. It returns the runtime endpoint and the API token
// its discovery record publishes.
//
// An explicit --port overrides endpoint discovery but still adopts the token if
// the project runtime published one, so an operator pointing at a known port is
// not locked out by authentication. Otherwise the project root is found by
// walking up from the current directory, and a missing runtime returns an empty
// endpoint rather than an error -- not running is the normal state before
// `neuron` starts.
func discoverLocalRuntime(cfg config.Config, overridePort string) (endpoint, token string, err error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", fmt.Errorf("get current directory: %w", err)
	}

	if overridePort != "" {
		host := cfg.Runtime.Host
		if host == "" {
			host = "127.0.0.1"
		}
		// Best effort: an explicit port is authoritative even if no discovery
		// record exists next to it.
		if record, found, err := findRuntimeRecord(cwd); err == nil && found {
			token = record.Token
		}
		return fmt.Sprintf("http://%s:%s", host, overridePort), token, nil
	}

	record, found, err := findRuntimeRecord(cwd)
	if err != nil {
		return "", "", err
	}
	if !found {
		return "", "", nil
	}
	return record.Endpoint(), record.Token, nil
}

// findRuntimeRecord walks up from startDir looking for a project runtime
// discovery record. It returns the first record it finds after validating it;
// a record left behind by a dead or foreign process is reported as an error
// rather than silently ignored, because it means the project believes a runtime
// is running when it is not.
func findRuntimeRecord(startDir string) (projectruntime.Record, bool, error) {
	root := filepath.Clean(startDir)
	for {
		path := projectruntime.Path(root)
		if _, err := os.Stat(path); err == nil {
			record, found, err := projectruntime.Read(root)
			if err != nil {
				return projectruntime.Record{}, false, err
			}
			if !found {
				return projectruntime.Record{}, false, nil
			}
			if err := validateRuntimeRecord(record, root); err != nil {
				return projectruntime.Record{}, false, err
			}
			return record, true, nil
		}
		parent := filepath.Dir(root)
		if parent == root {
			return projectruntime.Record{}, false, nil // reached the filesystem root
		}
		root = parent
	}
}

// validateRuntimeRecord rejects a record that cannot belong to a live runtime
// for this project.
func validateRuntimeRecord(record projectruntime.Record, root string) error {
	if record.PID <= 0 {
		return nil
	}
	if !isProcessAlive(record.PID) {
		return fmt.Errorf("runtime process %d is not alive", record.PID)
	}
	if record.ProjectRoot != "" && record.ProjectRoot != root {
		return fmt.Errorf("discovery record project root mismatch")
	}
	return nil
}

func isProcessAlive(pid int) bool {
	// On Unix, signal 0 checks if process exists without sending a signal
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 doesn't actually send a signal, just checks existence
	return proc.Signal(syscall.Signal(0)) == nil
}

// resolveCurrentBuildKey resolves the InstanceKey from the current build.
func resolveCurrentBuildKey(ctx context.Context, cfg config.Config) (protocol.InstanceKey, error) {
	root, err := os.Getwd()
	if err != nil {
		return protocol.InstanceKey{}, fmt.Errorf("get current directory: %w", err)
	}

	fingerprint, err := project.LoadCurrentPointer(root)
	if err != nil {
		if errors.Is(err, project.ErrNotBuilt) {
			return protocol.InstanceKey{}, fmt.Errorf("project has not been built; run `neuron build` first")
		}
		return protocol.InstanceKey{}, fmt.Errorf("load current pointer: %w", err)
	}

	var record project.BuildRecord
	if err := project.LoadBuildRecord(root, fingerprint, &record); err != nil {
		return protocol.InstanceKey{}, fmt.Errorf("load build record: %w", err)
	}
	return record.Key, nil
}

// outputResult writes the Assembly result to stdout in the specified format.
func outputResult(cmd *cobra.Command, result protocol.ExecutionResult, format string) error {
	if result.Error != "" {
		// Execution failed - output error to stderr and exit non-zero
		fmt.Fprintf(cmd.ErrOrStderr(), "execution failed: %s\n", result.Error)
		return errors.New("execution failed")
	}

	// Output the Assembly result to stdout
	switch format {
	case "json":
		data, err := json.MarshalIndent(result.Result, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal result: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(data))
	case "text":
		for k, v := range result.Result {
			fmt.Fprintf(cmd.OutOrStdout(), "%s: %v\n", k, v)
		}
	default:
		return fmt.Errorf("unknown format: %s", format)
	}
	return nil
}
