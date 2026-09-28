// Package build implements `neuron build`, the single command that takes a
// project from source to a registered, runnable assembly:
//
//	project source (TypeScript or YAML)
//	    → languages.Build          → .neuron/manifest.json  (canonical manifest)
//	    → catalog.BuildLocal       → installed local capability runtimes (immutable store)
//	    → compiler.Compile         → core.Assembly
//	    → resolve + freeze         → locked capability runtime set
//	    → register                 → N.O.R.E.
//	    → project.SaveBuildRecord  → .neuron/build.json     (build artifact)
//
// `neuron run` and future commands read .neuron/build.json to address the
// registered assembly and to detect staleness against the project's authoring
// fingerprint. `neuron register` is a deprecated alias of this command.
package build

import (
	"context"
	"fmt"
	"time"

	"github.com/Muhammad-Jay/neuron/application/build"
	"github.com/Muhammad-Jay/neuron/application/capabilityruntime"
	"github.com/Muhammad-Jay/neuron/application/compiler"
	"github.com/Muhammad-Jay/neuron/application/compiler/manifest"
	"github.com/Muhammad-Jay/neuron/application/config"
	"github.com/Muhammad-Jay/neuron/application/internal/cli/bootstrap"
	"github.com/Muhammad-Jay/neuron/application/internal/cli/command"
	"github.com/Muhammad-Jay/neuron/application/internal/cli/progress"
	"github.com/Muhammad-Jay/neuron/application/internal/cli/utils"
	"github.com/Muhammad-Jay/neuron/application/internal/runtimectl"
	"github.com/Muhammad-Jay/neuron/application/language"
	"github.com/Muhammad-Jay/neuron/application/project"
	capabilityrt "github.com/Muhammad-Jay/neuron/shared/types/capabilityruntime"
	"github.com/Muhammad-Jay/neuron/shared/types/core"
	"github.com/Muhammad-Jay/neuron/shared/types/protocol"
	"github.com/spf13/cobra"
)

// Options carries the command-line surface of `neuron build`. The deprecated
// `register` alias and `neuron run --build` both funnel through Options.
type Options struct {
	// Verbose toggles project-build verbose output.
	Verbose bool

	// Force replaces the registered assembly even when one exists.
	Force bool

	// Lang overrides the project authoring language. Empty resolves from the
	// effective configuration and the project layout.
	Lang string

	// Root is the honored project root. Empty uses the current directory.
	Root string
}

// New returns the `neuron build` command. It takes a project from source to a
// registered, runnable assembly: build the project to its canonical manifest,
// build and install any local capability runtimes, resolve and freeze the capability runtime set,
// and register the compiled assembly with N.O.R.E.
func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:   command.Build,
		Short: "Build the project and register it with N.O.R.E.",
		Long: "Build the project for the given authoring language, compile the resulting " +
			".neuron/manifest.json to a core.Assembly, build and install any local capability runtimes, " +
			"resolve and freeze the exact capability runtime set, and register the compiled assembly with " +
			"N.O.R.E. The outcome is persisted in .neuron/build.json, which `neuron run` uses " +
			"to detect staleness and address the registered assembly.",
		RunE: Handler,
	}

	f := cmd.Flags()
	f.StringP("lang", "l", "", "project authoring language (yaml, yml, typescript, ts)")
	f.StringP("root", "r", "", "project root (defaults to the current directory)")

	return cmd
}

// Handler is the shared `neuron build` implementation. The deprecated
// `neuron register` alias forwards to it.
func Handler(cmd *cobra.Command, args []string) error {
	verbose, _ := cmd.Flags().GetBool("verbose")
	force, _ := cmd.Flags().GetBool("force")
	langFlag, _ := cmd.Flags().GetString("lang")
	rootFlag, _ := cmd.Flags().GetString("root")

	return ExecuteWith(cmd, Options{
		Verbose: verbose,
		Force:   force,
		Lang:    langFlag,
		Root:    rootFlag,
	})
}

// ExecuteWith runs the full build pipeline against the effective configuration
// on ctx. It is the shared implementation used by `neuron build`, the
// deprecated `neuron register` alias, and `neuron run --build`.
func ExecuteWith(cmd *cobra.Command, opts Options) error {
	ctx := cmd.Context()

	cfg, ok := config.FromContext(ctx)
	if !ok {
		return fmt.Errorf("configuration not loaded")
	}

	root, err := utils.ResolveRoot(opts.Root)
	if err != nil {
		return err
	}

	lang, err := language.Resolve(opts.Lang, cfg.Lang, root)
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
		Verbose:   opts.Verbose,
		Out:       cmd.ErrOrStderr(),
	}); err != nil {
		return fmt.Errorf("build project: %w", err)
	}

	// Snapshot the authoring inputs consumed by this build. The fingerprint is
	// what `neuron run` compares against to decide whether the project changed
	// since it was last built.
	inputs, err := utils.AuthoringInputs(root, cfg)
	if err != nil {
		return err
	}
	fingerprint, err := project.ComputeFingerprint(inputs)
	if err != nil {
		return fmt.Errorf("fingerprint project inputs: %w", err)
	}

	// Build and install any local capability runtimes declared by the project before
	// resolution, so resolveFrozenCapabilityRuntimes operates on current artifacts.
	maxWorkers := cfg.Dev.MaxWorkers
	if maxWorkers < 1 {
		maxWorkers = 1
	}
	if err := buildLocalCapabilityRuntimes(ctx, cfg, root, opts.Force, maxWorkers, rep); err != nil {
		return err
	}

	// Compile the canonical manifest, resolve and freeze the capability runtime set, and
	// register the Assembly with N.O.R.E.
	result, resolved, err := register(ctx, cfg, root, opts.Verbose, opts.Force, rep)
	if err != nil {
		return err
	}

	return saveBuildRecord(root, fingerprint, result.Key, resolved)
}

// buildLocalCapabilityRuntimes materializes every local capability runtime under the project's
// local roots into the immutable store. CapabilityRuntimes lacking a payload run their
// declared build.command first; executers already installed are skipped with a
// cache note. A local capability runtime with neither a payload nor a build command is a
// hard error.
func buildLocalCapabilityRuntimes(ctx context.Context, cfg config.Config, root string, force bool, maxWorkers int, rep *progress.Reporter) error {
	// BuildLocal renders through its Status callback (one line per capability runtime
	// decision) so the shared Installer observer is not attached here; the
	// observer is reserved for the registry-resolution step, which would
	// otherwise double-report every local install.
	catalog, err := runtimectl.BuildCatalog(runtimectl.CatalogConfig{
		CapabilityRuntimesConfig: cfg.CapabilityRuntimes,
		ProjectRoot:              root,
	})
	if err != nil {
		return fmt.Errorf("build capability runtime catalog: %w", err)
	}

	if err := catalog.BuildLocal(ctx, runtimectl.BuildOptions{
		MaxWorkers:  maxWorkers,
		Force:       force,
		ProjectRoot: root,
		Status:      rep.Status,
	}); err != nil {
		return fmt.Errorf("build local capability runtimes: %w", err)
	}
	return nil
}

// saveBuildRecord persists the build outcome together with the authoring
// fingerprint that produced it. The legacy registration key file is written
// too so older tooling and docs keep working during the register→build rename.
func saveBuildRecord(root, fingerprint string, key protocol.InstanceKey, capabilityRuntimes []capabilityrt.ResolvedCapabilityRuntime) error {
	record := project.BuildRecord{
		Fingerprint:        fingerprint,
		Key:                key,
		BuiltAt:            time.Now().UTC(),
		CapabilityRuntimes: capabilityRuntimes,
	}
	if err := project.SaveBuildRecord(root, record); err != nil {
		return fmt.Errorf("save build record: %w", err)
	}

	// Migration window artifact: keep `neuron register` standalone consumers
	// working until docs/tests are migrated to `neuron build`.
	if err := project.SaveRegistrationKey(root, key); err != nil {
		return fmt.Errorf("save registration key: %w", err)
	}

	return nil
}

// register performs the compile + register flow against the manifest already
// produced at root. It returns the registration response and the frozen
// capability runtime set used to serve executions.
func register(ctx context.Context, cfg config.Config, root string, verbose bool, force bool, rep capabilityruntime.Observer) (*protocol.RegisterResponse, []capabilityrt.ResolvedCapabilityRuntime, error) {
	c, cleanup, err := bootstrap.SetupClient(ctx, bootstrap.Options{Config: cfg})
	if err != nil {
		return nil, nil, err
	}
	defer cleanup()

	// Load the canonical manifest produced by the build step.
	m, err := manifest.LoadFromProjectRoot(root)
	if err != nil {
		return nil, nil, fmt.Errorf("load manifest (a `%s` build must write .neuron/manifest.json): %w", command.Build, err)
	}

	// Compile the manifest into the runtime core.Assembly representation.
	comp := compiler.New()
	sys, err := comp.Compile(m)
	if err != nil {
		return nil, nil, fmt.Errorf("compile manifest: %w", err)
	}

	// Compute the instance key from the manifest + compiled assembly. The execution
	// environment comes from the effective config, not the manifest.
	key, err := comp.InstanceKey(m, cfg.Runtime.Execution.Mode)
	if err != nil {
		return nil, nil, fmt.Errorf("compute instance key: %w", err)
	}

	configs := buildExecutionConfigurations(cfg, m)

	// Resolve the capability runtime requirements declared by capabilities and freeze the
	// exact dependency set into the register payload, so N.O.R.E. can launch
	// Instances without resolving or installing anything itself.
	resolved, err := resolveFrozenCapabilityRuntimes(ctx, cfg, configs.CapabilityRuntimeRequirements, rep)
	if err != nil {
		return nil, nil, err
	}
	configs.ResolvedCapabilityRuntimes = resolved

	request := protocol.RegisterRequest{
		Key:                     key,
		Assembly:                *sys,
		ExecutionConfigurations: configs,
		Force:                   force,
	}

	result, err := c.Register(ctx, request)
	if err != nil {
		return nil, nil, err
	}

	if err := project.SaveRegistrationKey(root, result.Key); err != nil {
		return nil, nil, err
	}

	printRegistration(result)

	return &result, resolved, nil
}

// printRegistration writes the registration record (assembly, version, frozen
// hash, environment) to stdout.
func printRegistration(result protocol.RegisterResponse) {
	line := fmt.Sprintf("%s@%s#%s:%s", result.Key.AssemblyID, result.Key.Version, result.Key.Hash, result.Key.Env)
	if result.Status != "" {
		line += fmt.Sprintf(" (%s)", result.Status)
	}
	fmt.Println(line)
}

// buildExecutionConfigurations assembles the N.O.R.E. payload from the
// effective configuration and the compiled manifest. The configuration, not
// the manifest, is the single source of truth for registries, runtime,
// storage, and inspector settings; the manifest contributes the capability runtime
// requirements indexed from its capabilities.
func buildExecutionConfigurations(cfg config.Config, m *manifest.Assembly) compiler.ExecutionConfigurations {
	registries := make([]manifest.CapabilityRuntimeRegistry, 0, len(cfg.CapabilityRuntimes.Registries))
	for _, reg := range cfg.CapabilityRuntimes.Registries {
		registries = append(registries, manifest.CapabilityRuntimeRegistry{
			Name: reg.Name,
			URL:  reg.URL,
		})
	}

	return compiler.ExecutionConfigurations{
		CapabilityRuntimeRegistries:   registries,
		CapabilityRuntimeRequirements: compiler.CapabilityRuntimeRequirements(m.Capabilities),
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

// resolveFrozenCapabilityRuntimes resolves each capability runtime requirement through the wired
// catalog and freezes the results into the wire format persisted in a
// Deployment.
func resolveFrozenCapabilityRuntimes(ctx context.Context, cfg config.Config, requirements []manifest.CapabilityRuntimeRequirement, observer capabilityruntime.Observer) ([]capabilityrt.ResolvedCapabilityRuntime, error) {
	if len(requirements) == 0 {
		return nil, nil
	}

	catalog, err := runtimectl.BuildCatalog(runtimectl.CatalogConfig{
		CapabilityRuntimesConfig: cfg.CapabilityRuntimes,
		ProjectRoot:              cfg.ProjectDir,
		Observer:                 observer,
	})
	if err != nil {
		return nil, fmt.Errorf("build capability runtime catalog: %w", err)
	}

	capabilityRuntimeReqs := make([]capabilityruntime.Requirement, 0, len(requirements))
	for _, req := range requirements {
		// Core capability runtimes (neuron:core:*) run in-process inside N.O.R.E. and
		// the legacy bare core names too; there is nothing to resolve or
		// install for them.
		if core.IsCoreRuntimeType(core.CapabilityRuntimeType(req.Name)) {
			continue
		}
		var registries []string
		if req.Registry != "" {
			registries = []string{req.Registry}
		}
		capabilityRuntimeReqs = append(capabilityRuntimeReqs, catalog.Require(req.Name, req.Version, registries))
	}

	env, err := catalog.Resolve(ctx, capabilityRuntimeReqs)
	if err != nil {
		return nil, fmt.Errorf("resolve capability runtimes: %w", err)
	}

	requested := make(map[string]string, len(capabilityRuntimeReqs))
	for _, req := range capabilityRuntimeReqs {
		requested[req.Type] = req.Version
	}

	frozen := make([]capabilityrt.ResolvedCapabilityRuntime, 0, len(env.CapabilityRuntimes))
	for _, installed := range env.CapabilityRuntimes {
		frozen = append(frozen, *installed.Frozen(requested[installed.Type]))
	}
	return frozen, nil
}
