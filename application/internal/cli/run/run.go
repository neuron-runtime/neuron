package run

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
		target = args[0]
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
	// parameter. If an explicit target is provided, try to interpret it as an
	// assembly name from the current build; otherwise pass it through as an
	// existing instance key/selector.
	selector, err := resolveSelector(cfg, target)
	if err != nil {
		return err
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
// discovery record. It returns the newest live record for the nearest project
// that has one. A record left behind by a dead or foreign process is reported as
// an error rather than silently ignored, because it means the project believes a
// runtime is running when it is not.
func findRuntimeRecord(startDir string) (projectruntime.Record, bool, error) {
	root := filepath.Clean(startDir)
	for {
		record, err := selectRuntimeRecord(root)
		if err != nil {
			return projectruntime.Record{}, false, err
		}
		if record != nil {
			return *record, true, nil
		}
		parent := filepath.Dir(root)
		if parent == root {
			return projectruntime.Record{}, false, nil // reached the filesystem root
		}
		root = parent
	}
}

// selectRuntimeRecord returns the newest live runtime record for a project root,
// or nil when the project has no runtime record at all. Several sessions may
// publish records for the same project; the newest whose process is still alive
// and whose project root matches wins. If records exist but none is usable, the
// newest unusable record is reported as an error so the caller learns the
// project's runtime is stale rather than absent.
func selectRuntimeRecord(root string) (*projectruntime.Record, error) {
	records, err := projectruntime.ReadAll(root)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, nil
	}
	for i := range records {
		if err := validateRuntimeRecord(records[i], root); err == nil {
			return &records[i], nil
		}
	}
	return nil, validateRuntimeRecord(records[0], root)
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

// resolveSelector determines the selector to pass to the runtime facade:
// if target is empty, it selects the default Assembly from the current build;
// if target names a built Assembly, it returns its key colon form; otherwise it
// normalizes the target as an instance key/selector (preserving legacy behavior).
func resolveSelector(cfg config.Config, target string) (string, error) {
	root, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get current directory: %w", err)
	}

	if target == "" {
		return currentDefaultKey(root, cfg)
	}

	// A name that matches a built Assembly selects it. Instance IDs (inst_*)
	// never name an Assembly, so they skip the lookup and are normalized as
	// selectors along with any other explicit target.
	if !strings.HasPrefix(target, "inst_") {
		if key, ok := findAssemblyKey(root, target); ok {
			return key.ColonString(), nil
		}
	}

	norm, err := utils.NormalizeInstanceTarget(target)
	if err != nil {
		return "", err
	}
	return norm, nil
}

func findAssemblyKey(root, name string) (protocol.InstanceKey, bool) {
	fp, err := project.LoadCurrentPointer(root)
	if err != nil {
		return protocol.InstanceKey{}, false
	}
	var record project.BuildRecord
	if err := project.LoadBuildRecord(root, fp, &record); err != nil {
		return protocol.InstanceKey{}, false
	}
	if a, ok := record.FindAssembly(name); ok {
		return a.Key, true
	}
	return protocol.InstanceKey{}, false
}

func currentDefaultKey(root string, cfg config.Config) (string, error) {
	fp, err := project.LoadCurrentPointer(root)
	if err != nil {
		if errors.Is(err, project.ErrNotBuilt) {
			return "", fmt.Errorf("project has not been built; run `neuron build` first")
		}
		return "", fmt.Errorf("load current pointer: %w", err)
	}
	var record project.BuildRecord
	if err := project.LoadBuildRecord(root, fp, &record); err != nil {
		return "", fmt.Errorf("load build record: %w", err)
	}

	switch len(record.Assemblies) {
	case 0:
		return "", fmt.Errorf("current build records no Assemblies")
	case 1:
		return record.Assemblies[0].Key.ColonString(), nil
	default:
		// Select the default Assembly; if none is marked, require explicit name.
		if def, ok := cfg.DefaultAssembly(); ok {
			if a, ok := record.FindAssembly(def.Name); ok {
				return a.Key.ColonString(), nil
			}
			return "", fmt.Errorf("default Assembly %q not found in current build", def.Name)
		}
		names := make([]string, 0, len(record.Assemblies))
		for _, a := range record.Assemblies {
			names = append(names, a.Name)
		}
		return "", fmt.Errorf("multiple Assemblies built (%v); specify one as an argument", names)
	}
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
