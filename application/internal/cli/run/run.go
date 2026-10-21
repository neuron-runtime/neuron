package run

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/neuron-runtime/neuron/application/capabilityruntime"
	"github.com/neuron-runtime/neuron/application/capabilityruntime/store"
	"github.com/neuron-runtime/neuron/application/client"
	"github.com/neuron-runtime/neuron/application/config"
	"github.com/neuron-runtime/neuron/application/connection"
	"github.com/neuron-runtime/neuron/application/internal/cli/bootstrap"
	"github.com/neuron-runtime/neuron/application/internal/cli/command"
	"github.com/neuron-runtime/neuron/application/internal/cli/utils"
	"github.com/neuron-runtime/neuron/application/internal/ref"
	"github.com/neuron-runtime/neuron/application/project"
	core "github.com/neuron-runtime/neuron/shared/types/core"
	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
	"github.com/neuron-runtime/neuron/shared/types/projectruntime"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
	"github.com/spf13/cobra"
)

var (
	params            string
	paramsFile        string
	overridePort      string
	format            string
	capabilityRuntime string
)

// New constructs and configures the Cobra command for executing Neuron assemblies.
func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:   command.Run,
		Short: "Run a Neuron Assembly or an installed Capability Runtime",
		Long: "Run a Neuron Assembly using the project runtime, or run a Capability Runtime\n" +
			"directly with --capability-runtime — a built-in core runtime (neuron:core:set) or an\n" +
			"installed one (name[@version]). Assembly execution discovers the local runtime or\n" +
			"connects to a remote endpoint; direct invocation needs no project or running runtime.",
		RunE: runCmdHandler,
	}

	cmd.Flags().StringVar(&params, "params", "", "JSON params payload for execution (e.g., '{\"key\":\"value\"}')")
	cmd.Flags().StringVar(&paramsFile, "params-file", "", "Path to JSON file with params payload")
	cmd.Flags().StringVarP(&capabilityRuntime, "capability-runtime", "c", "", "run a Capability Runtime directly: a built-in core runtime (neuron:core:set) or an installed one (name[@version])")
	cmd.Flags().StringVar(&overridePort, "port", "", "Override local runtime port (default: auto-discover from project)")
	cmd.Flags().StringVar(&format, "format", "json", "Output format for the result (json, text)")

	return cmd
}

// DirectMode reports whether cmd is a `neuron run` invocation in direct
// Capability Runtime mode. Only the run command defines --capability-runtime, so
// the flag's presence identifies it. In direct mode the caller is
// project-independent and no project configuration may be loaded.
func DirectMode(cmd *cobra.Command) bool {
	if cmd.Flags().Lookup("capability-runtime") == nil {
		return false
	}
	value, err := cmd.Flags().GetString("capability-runtime")
	return err == nil && value != ""
}

// runCmdHandler runs an Assembly using the project runtime, or a single
// installed Capability Runtime in direct mode. The two modes are explicit and
// mutually exclusive; each owns its own resolution path but shares the params
// and result handling below.
func runCmdHandler(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	// The remote endpoint is the global --remote flag, read directly so run does
	// not shadow the root flag with a second identical definition.
	remoteURL, _ := cmd.Flags().GetString("remote")

	// Direct Capability Runtime invocation is a distinct mode: it selects the
	// artifact by reference rather than by Assembly, and never touches a project
	// runtime. Conflicting selectors are rejected rather than guessed at.
	if capabilityRuntime != "" {
		if len(args) > 0 {
			return fmt.Errorf("cannot combine a target (%q) with --capability-runtime; choose one invocation mode", args[0])
		}
		if remoteURL != "" {
			return fmt.Errorf("--remote cannot be combined with --capability-runtime; direct invocation always runs locally")
		}
		if overridePort != "" {
			return fmt.Errorf("--port cannot be combined with --capability-runtime; direct invocation does not use a project runtime")
		}
		return runDirect(cmd, capabilityRuntime)
	}

	cfg, ok := config.FromContext(ctx)
	if !ok {
		return fmt.Errorf("configuration not loaded")
	}

	// Resolve target assembly if provided
	target := ""
	if len(args) > 0 {
		target = args[0]
	}

	// Load params from --params or --params-file
	execParams, err := loadExecParams()
	if err != nil {
		return err
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

// runDirect runs a single Capability Runtime without a project, a build, or a
// running project runtime. Built-in core runtimes (neuron:core:set) are
// compiled into N.O.R.E. and are executed in-process by the child; installed
// artifacts are resolved from the local store only — never installing or
// fetching anything — and hosted through a one-shot `nore --invoke` child
// process, which selects the provider from the artifact's declared runtime
// kind using the same registry as Assembly execution.
//
// The reference is `name[@version]` (or a bare/core name such as `set`). A
// version may be an exact pin, a constraint, or `latest`; it is resolved to an
// exact installed identity before invocation. The successful result is written
// to stdout; diagnostics and failures go to stderr with a non-zero exit.
func runDirect(cmd *cobra.Command, reference string) error {
	ctx := cmd.Context()

	typ, version, ok := ref.Parse(reference)
	if !ok {
		return fmt.Errorf("capability runtime name is required (e.g. neuron:core:set)")
	}

	execParams, err := loadExecParams()
	if err != nil {
		return err
	}

	var req capabilityrt.InvokeRequest
	req.Params = execParams

	// Core capability runtimes are compiled into N.O.R.E., so they are never
	// resolved from the store and never carry a version; installed artifacts
	// resolve from the machine-global store only.
	var storeDir string
	if coreType := core.CapabilityRuntimeType(typ); core.IsCoreRuntimeType(coreType) {
		if version != "" {
			return fmt.Errorf("core capability runtime %s has no versions; drop @%s and invoke it as %q",
				typ, version, core.CanonicalCoreType(coreType))
		}
		req.CoreType = string(core.CanonicalCoreType(coreType))
	} else {
		// Direct invocation is project-independent, so it resolves against the
		// default machine-global store — exactly where `neuron add` installs when
		// no store directory is configured. Requiring the artifact to be installed
		// keeps direct invocation free of implicit resolution or installation.
		storeDir = config.DefaultStoreDir()
		fsStore, err := store.NewFileassemblyStore(storeDir)
		if err != nil {
			return err
		}

		installed, err := capabilityruntime.ResolveInstalled(ctx, fsStore, typ, version)
		if err != nil {
			return err
		}
		req.CapabilityRuntime = *installed.Frozen(version)
	}

	norePath, _ := cmd.Flags().GetString("nore-path")
	noreBin, err := bootstrap.NoreBinaryPathFromPath(norePath)
	if err != nil {
		return fmt.Errorf("find nore binary: %w", err)
	}

	requestJSON, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("encode invocation request: %w", err)
	}

	// The store flag only matters for installed artifacts; the child's default
	// store root is never consulted for a core runtime.
	childArgs := []string{"--invoke"}
	if storeDir != "" {
		childArgs = append(childArgs, "--capability-runtime-store", storeDir)
	}
	child := exec.CommandContext(ctx, noreBin, childArgs...)
	child.Stdin = bytes.NewReader(requestJSON)
	child.Stderr = cmd.ErrOrStderr()

	var stdout bytes.Buffer
	child.Stdout = &stdout

	runErr := child.Run()

	// The child always writes exactly one InvokeResponse to stdout, even when it
	// fails, so a structured error is the most precise diagnostic. The child's
	// exit error is used only when no response could be parsed.
	var resp capabilityrt.InvokeResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		if runErr != nil {
			return fmt.Errorf("invoke capability runtime %s: %w", reference, runErr)
		}
		return fmt.Errorf("decode invocation response from nore: %w", err)
	}
	if resp.Error != "" {
		return fmt.Errorf("invoke capability runtime %s: %s", reference, resp.Error)
	}
	if runErr != nil {
		return fmt.Errorf("invoke capability runtime %s: %w", reference, runErr)
	}

	return writeResult(cmd, resp.Result, format)
}

// loadExecParams reads the execution params from --params or --params-file,
// which are mutually exclusive. It returns an empty payload when neither is
// set.
func loadExecParams() (map[string]any, error) {
	if params != "" && paramsFile != "" {
		return nil, fmt.Errorf("--params and --params-file are mutually exclusive")
	}
	if paramsFile != "" {
		data, err := os.ReadFile(paramsFile)
		if err != nil {
			return nil, fmt.Errorf("read params file: %w", err)
		}
		var out map[string]any
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, fmt.Errorf("invalid params file JSON: %w", err)
		}
		return out, nil
	}
	if params != "" {
		var out map[string]any
		if err := json.Unmarshal([]byte(params), &out); err != nil {
			return nil, fmt.Errorf("invalid --params JSON: %w", err)
		}
		return out, nil
	}
	return map[string]any{}, nil
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
	return writeResult(cmd, result.Result, format)
}

// writeResult writes a result map to stdout in the selected format. It is the
// single result-rendering path shared by Assembly execution and direct
// Capability Runtime invocation.
func writeResult(cmd *cobra.Command, result map[string]any, format string) error {
	switch format {
	case "json":
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal result: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(data))
	case "text":
		for k, v := range result {
			fmt.Fprintf(cmd.OutOrStdout(), "%s: %v\n", k, v)
		}
	default:
		return fmt.Errorf("unknown format: %s", format)
	}
	return nil
}
