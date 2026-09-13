package register

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Muhammad-Jay/neuron/application/build"
	"github.com/Muhammad-Jay/neuron/application/compiler"
	"github.com/Muhammad-Jay/neuron/application/compiler/manifest"
	"github.com/Muhammad-Jay/neuron/application/config"
	"github.com/Muhammad-Jay/neuron/application/executor"
	"github.com/Muhammad-Jay/neuron/application/internal/cli/bootstrap"
	"github.com/Muhammad-Jay/neuron/application/internal/cli/command"
	"github.com/Muhammad-Jay/neuron/application/internal/cli/progress"
	"github.com/Muhammad-Jay/neuron/application/internal/executorctl"
	"github.com/Muhammad-Jay/neuron/application/language"
	"github.com/Muhammad-Jay/neuron/application/project"
	"github.com/Muhammad-Jay/neuron/shared/types/core"
	shadexec "github.com/Muhammad-Jay/neuron/shared/types/executor"
	"github.com/Muhammad-Jay/neuron/shared/types/protocol"
	"github.com/spf13/cobra"
)

// New returns the `neuron register` command. It builds the project (YAML or
// TypeScript) and registers the resulting manifest with N.O.R.E. in one step.
func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:   command.Register,
		Short: "Build and register the current project to N.O.R.E",
		Long:  "Build the project for the given authoring language, compile the resulting .neuron/manifest.json to a core.System, and register it with N.O.R.E. ",
		RunE:  registerCmdHandler,
	}

	f := cmd.Flags()
	f.StringP("lang", "l", "", "project authoring language (yaml, yml, typescript, ts)")
	f.StringP("root", "r", "", "project root (defaults to the current directory)")

	return cmd
}

func registerCmdHandler(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	cfg, ok := config.FromContext(ctx)
	if !ok {
		return fmt.Errorf("configuration not loaded")
	}

	verbose, _ := cmd.Flags().GetBool("verbose")
	force, _ := cmd.Flags().GetBool("force")
	langFlag, _ := cmd.Flags().GetString("lang")
	rootFlag, _ := cmd.Flags().GetString("root")

	root, err := resolveRoot(rootFlag)
	if err != nil {
		return err
	}

	lang, err := language.Resolve(langFlag, cfg.Lang, root)
	if err != nil {
		return err
	}

	// Progress output lives on stderr so stdout stays reserved for the
	// registration record printed at the end.
	rep := progress.New(cmd.ErrOrStderr())
	defer rep.Stop()

	// Build the project into the canonical .neuron/manifest.json for the
	// resolved language. Configuration is the single source of truth for the
	// entry file and project variables.
	if err := build.Build(ctx, lang, build.Options{
		Root:      root,
		Entry:     cfg.Entry,
		Variables: cfg.Variables,
		Verbose:   verbose,
		Out:       cmd.ErrOrStderr(),
	}); err != nil {
		return fmt.Errorf("build project: %w", err)
	}

	return register(ctx, cfg, root, verbose, force, rep)
}

// register performs the compile + resolve + register flow against the manifest
// already produced at root.
func register(ctx context.Context, cfg config.Config, root string, verbose bool, force bool, rep executor.Observer) error {
	c, cleanup, err := bootstrap.SetupClient(ctx, bootstrap.Options{Config: cfg})
	if err != nil {
		return err
	}
	defer cleanup()

	// Load the canonical manifest produced by the build step.
	m, err := manifest.LoadFromProjectRoot(root)
	if err != nil {
		return fmt.Errorf("load manifest (a `%s` build must write .neuron/manifest.json): %w", "neuron register", err)
	}

	// Compile the manifest into the runtime core.System representation.
	comp := compiler.New()
	sys, err := comp.Compile(m)
	if err != nil {
		return fmt.Errorf("compile manifest: %w", err)
	}

	// Compute the instance key from the manifest + compiled system. The execution
	// environment comes from the effective config, not the manifest.
	key, err := comp.InstanceKey(m, cfg.Runtime.Execution.Mode)
	if err != nil {
		return fmt.Errorf("compute instance key: %w", err)
	}

	configs := buildExecutionConfigurations(cfg, m)

	// Resolve the executor requirements declared by services and freeze the
	// exact dependency set into the register payload, so N.O.R.E. can launch
	// Instances without resolving or installing anything itself.
	resolved, err := resolveFrozenExecutors(ctx, cfg, configs.ExecutorRequirements, rep)
	if err != nil {
		return err
	}
	configs.ResolvedExecutors = resolved

	request := protocol.RegisterRequest{
		Key:                     key,
		System:                  *sys,
		ExecutionConfigurations: configs,
		Force:                   force,
	}

	result, err := c.Register(ctx, request)
	if err != nil {
		return err
	}

	if err := project.SaveRegistrationKey(root, result.Key); err != nil {
		return err
	}

	printRegistration(result)

	return nil
}

// resolveRoot normalizes the honored project root: flag first, cwd fallback.
func resolveRoot(flag string) (string, error) {
	root := flag
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("get current directory: %w", err)
		}
		root = cwd
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve project root %q: %w", root, err)
	}
	return abs, nil
}

// printRegistration writes the registration record (system, version, frozen
// hash, environment) to stdout.
func printRegistration(result protocol.RegisterResponse) {
	line := fmt.Sprintf("%s@%s#%s:%s", result.Key.SystemID, result.Key.Version, result.Key.Hash, result.Key.Env)
	if result.Status != "" {
		line += fmt.Sprintf(" (%s)", result.Status)
	}
	fmt.Println(line)
}

// buildExecutionConfigurations assembles the N.O.R.E. payload from the
// effective configuration and the compiled manifest. The configuration, not
// the manifest, is the single source of truth for registries, runtime,
// storage, and inspector settings; the manifest contributes the executor
// requirements indexed from its services.
func buildExecutionConfigurations(cfg config.Config, m *manifest.System) compiler.ExecutionConfigurations {
	registries := make([]manifest.ExecutorRegistry, 0, len(cfg.Executors.Registries))
	for _, reg := range cfg.Executors.Registries {
		registries = append(registries, manifest.ExecutorRegistry{
			Name: reg.Name,
			URL:  reg.URL,
		})
	}

	return compiler.ExecutionConfigurations{
		ExecutorRegistries:   registries,
		ExecutorRequirements: compiler.ExecutorRequirements(m.Services),
		Runtime: manifest.RuntimeConfig{
			Execution: manifest.RuntimeExecutionConfig{
				Mode:    cfg.Runtime.Execution.Mode,
				Timeout: cfg.Runtime.Execution.Timeout,
			},
			Workers: manifest.WorkerConfig{
				Min: cfg.Runtime.Workers.Min,
				Max: cfg.Runtime.Workers.Max,
			},
		},
		Storage: manifest.StorageConfig{
			Provider:  cfg.Storage.Provider,
			Directory: cfg.Storage.Directory,
		},
		Inspector: manifest.InspectorConfig{
			Enabled: cfg.Inspector.Enabled,
			Address: cfg.Inspector.Address,
		},
	}
}

// resolveFrozenExecutors resolves each executor requirement through the wired
// catalog and freezes the results into the wire format persisted in a
// Deployment.
func resolveFrozenExecutors(ctx context.Context, cfg config.Config, requirements []manifest.ExecutorRequirement, observer executor.Observer) ([]shadexec.ResolvedExecutor, error) {
	if len(requirements) == 0 {
		return nil, nil
	}

	catalog, err := executorctl.BuildCatalog(executorctl.CatalogConfig{
		ExecutorsConfig: cfg.Executors,
		Observer:        observer,
	})
	if err != nil {
		return nil, fmt.Errorf("build executor catalog: %w", err)
	}

	executorReqs := make([]executor.Requirement, 0, len(requirements))
	for _, req := range requirements {
		// Core executors (neuron:core:*) run in-process inside N.O.R.E. and
		// the legacy bare core names too; there is nothing to resolve or
		// install for them.
		if core.IsCoreExecutorType(core.ExecutorType(req.Name)) {
			continue
		}
		var registries []string
		if req.Registry != "" {
			registries = []string{req.Registry}
		}
		executorReqs = append(executorReqs, catalog.Require(req.Name, req.Version, registries))
	}

	env, err := catalog.Resolve(ctx, executorReqs)
	if err != nil {
		return nil, fmt.Errorf("resolve executors: %w", err)
	}

	requested := make(map[string]string, len(executorReqs))
	for _, req := range executorReqs {
		requested[req.Type] = req.Version
	}

	frozen := make([]shadexec.ResolvedExecutor, 0, len(env.Executors))
	for _, installed := range env.Executors {
		frozen = append(frozen, *installed.Frozen(requested[installed.Type]))
	}
	return frozen, nil
}
