// Package build implements `neuron build`, the single command that takes a
// project from source to registered, runnable Assemblies:
//
//	project source (TypeScript or YAML)
//	    → languages.Build          → .neuron/assemblies/<name>/manifest.json  (per Assembly)
//	    → catalog.BuildLocal       → installed local capability runtimes (immutable store)
//	    → compiler.Compile         → core.Assembly
//	    → resolve + freeze         → locked capability runtime set
//	    → freeze providers         → locked provider identities (store-only)
//	    → register                 → N.O.R.E.
//	    → project.SaveBuildRecord  → .neuron/builds/<fingerprint>/build.json
//
// A project may declare several Assemblies. Each is compiled, registered, and
// persisted independently under its own identity while the project keeps one
// immutable build record referencing all of them. `neuron run` and future
// commands read that record to address a registered Assembly and to select the
// default one. `neuron register` is a deprecated alias of this command.
package build

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/neuron-runtime/neuron/application/build"
	"github.com/neuron-runtime/neuron/application/capabilityruntime"
	"github.com/neuron-runtime/neuron/application/client"
	"github.com/neuron-runtime/neuron/application/compiler"
	"github.com/neuron-runtime/neuron/application/compiler/manifest"
	"github.com/neuron-runtime/neuron/application/config"
	"github.com/neuron-runtime/neuron/application/internal/cli/bootstrap"
	"github.com/neuron-runtime/neuron/application/internal/cli/command"
	"github.com/neuron-runtime/neuron/application/internal/cli/progress"
	"github.com/neuron-runtime/neuron/application/internal/cli/utils"
	"github.com/neuron-runtime/neuron/application/internal/providerctl"
	"github.com/neuron-runtime/neuron/application/internal/runtimectl"
	"github.com/neuron-runtime/neuron/application/language"
	"github.com/neuron-runtime/neuron/application/project"
	"github.com/neuron-runtime/neuron/application/provider"
	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
	"github.com/neuron-runtime/neuron/shared/types/core"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
	providertypes "github.com/neuron-runtime/neuron/shared/types/provider"
	"github.com/spf13/cobra"
)

// Options carries the command-line surface of `neuron build`. The deprecated
// `register` alias and `neuron run --build` both funnel through Options.
type Options struct {
	// Verbose toggles project-build verbose output.
	Verbose bool

	// Force replaces a registered Assembly even when one exists.
	Force bool

	// Lang overrides the project authoring language. Empty resolves from the
	// effective configuration and the project layout.
	Lang string

	// Root is the honored project root. Empty uses the current directory.
	Root string

	// Assembly selects one declared Assembly to (re)build. Empty builds every
	// declared Assembly.
	Assembly string
}

// New returns the `neuron build` command. It takes a project from source to
// registered, runnable Assemblies: build each declared Assembly to its
// canonical manifest, build and install any local capability runtimes, resolve
// and freeze each Assembly's capability runtime set, and register it with
// N.O.R.E. The outcome is persisted in an immutable build record under
// .neuron/builds, which `neuron run` uses to address a registered Assembly.
func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:   command.Build,
		Short: "Build the project and register its Assemblies with the Neuron runtime.",
		Long: "Build every declared Assembly for the given authoring language, compile each " +
			"manifest to a core.Assembly, build and install any local capability runtimes, " +
			"resolve and freeze each Assembly's exact capability runtime set, and register the " +
			"compiled Assemblies with the Neuron runtime. The outcome is persisted in .neuron/builds, which " +
			"`neuron run` uses to address a registered Assembly.",
		RunE: Handler,
	}

	f := cmd.Flags()
	f.StringP("lang", "l", "", "project authoring language (yaml, yml, typescript, ts)")
	f.StringP("root", "r", "", "project root (defaults to the current directory)")
	f.StringP("assembly", "a", "", "build only the named Assembly (default: all declared Assemblies)")

	return cmd
}

// Handler is the shared `neuron build` implementation. The deprecated
// `neuron register` alias forwards to it.
func Handler(cmd *cobra.Command, args []string) error {
	verbose, _ := cmd.Flags().GetBool("verbose")
	force, _ := cmd.Flags().GetBool("force")
	langFlag, _ := cmd.Flags().GetString("lang")
	rootFlag, _ := cmd.Flags().GetString("root")
	assemblyFlag, _ := cmd.Flags().GetString("assembly")

	return ExecuteWith(cmd, Options{
		Verbose:  verbose,
		Force:    force,
		Lang:     langFlag,
		Root:     rootFlag,
		Assembly: assemblyFlag,
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
	// registration records printed at the end.
	rep := progress.New(cmd.ErrOrStderr())
	defer rep.Stop()

	declarations := cfg.EffectiveAssemblies()
	if len(declarations) == 0 {
		return fmt.Errorf("project declares no Assemblies: add an `assemblies` list to neuron.config.*")
	}

	// Resolve and validate every declared entry path before building anything,
	// so an invalid path is reported up front rather than after partial work.
	entries := make([]string, len(declarations))
	for i, decl := range declarations {
		entry, err := resolveAssemblyEntry(root, lang, decl.Entry)
		if err != nil {
			return fmt.Errorf("assembly %q: %w", decl.Name, err)
		}
		entries[i] = entry
	}

	if opts.Assembly != "" {
		if _, ok := findDeclaration(declarations, opts.Assembly); !ok {
			return fmt.Errorf("no Assembly named %q is declared by the project", opts.Assembly)
		}
	}

	// Build and install any local capability runtimes declared by the project
	// before resolution, so resolveFrozenCapabilityRuntimes operates on current
	// artifacts. Local capability runtimes are shared across Assemblies.
	maxWorkers := cfg.Dev.MaxWorkers
	if maxWorkers < 1 {
		maxWorkers = 1
	}
	if err := buildLocalCapabilityRuntimes(ctx, cfg, root, opts.Force, maxWorkers, rep); err != nil {
		return err
	}

	// Carry forward unrelated Assemblies when a single Assembly is selected.
	var previous *project.BuildRecord
	if fp, err := project.LoadCurrentPointer(root); err == nil {
		var rec project.BuildRecord
		if err := project.LoadBuildRecord(root, fp, &rec); err == nil {
			previous = &rec
		}
	}

	// One runtime client serves every registration in this build.
	c, cleanup, err := bootstrap.SetupClient(ctx, bootstrap.Options{Config: cfg})
	if err != nil {
		return err
	}
	defer cleanup()

	builds := make([]project.AssemblyBuild, 0, len(declarations))
	for i, decl := range declarations {
		entry := entries[i]

		inputs, err := utils.AuthoringInputs(root, cfg, entry)
		if err != nil {
			return err
		}
		fingerprint, err := project.ComputeFingerprint(inputs)
		if err != nil {
			return fmt.Errorf("fingerprint assembly %q: %w", decl.Name, err)
		}

		selected := opts.Assembly == "" || opts.Assembly == decl.Name
		if !selected {
			// A selective build never rebuilds an unrelated Assembly; it
			// carries the previously built entry forward unchanged. With no
			// prior build to reuse it falls through and builds it, so the
			// record stays complete even though it was not explicitly selected.
			if prev, ok := carryForward(previous, decl.Name); ok {
				builds = append(builds, prev)
				continue
			}
		}

		m, err := build.BuildAssembly(ctx, lang, build.Options{
			Root:      root,
			Entry:     entry,
			Name:      decl.Name,
			Version:   cfg.Version,
			Variables: cfg.Variables,
			Verbose:   opts.Verbose,
			Out:       cmd.ErrOrStderr(),
		})
		if err != nil {
			return fmt.Errorf("build assembly %q: %w", decl.Name, err)
		}

		if err := manifest.SaveAssembly(root, decl.Name, m); err != nil {
			return fmt.Errorf("persist assembly %q manifest: %w", decl.Name, err)
		}

		result, resolved, providers, err := register(ctx, c, cfg, m, opts.Force)
		if err != nil {
			return fmt.Errorf("register assembly %q: %w", decl.Name, err)
		}

		builds = append(builds, project.AssemblyBuild{
			Name:               decl.Name,
			ManifestPath:       relativeManifestPath(decl.Name),
			Fingerprint:        fingerprint,
			Key:                result.Key,
			CapabilityRuntimes: resolved,
			Providers:          providers,
		})
	}

	projectFingerprint, err := projectFingerprint(root, cfg, entries)
	if err != nil {
		return err
	}

	return saveBuildRecord(root, projectFingerprint, builds)
}

// resolveAssemblyEntry resolves an Assembly entry to an absolute path and
// verifies it exists. An empty entry resolves to the language default.
func resolveAssemblyEntry(root string, lang language.Language, entry string) (string, error) {
	if entry == "" {
		entry = lang.DefaultEntry()
	}
	if !filepath.IsAbs(entry) {
		entry = filepath.Join(root, entry)
	}
	entry = filepath.Clean(entry)
	if _, err := os.Stat(entry); err != nil {
		return "", fmt.Errorf("entry %s: %w", entry, err)
	}
	return entry, nil
}

// carryForward returns the previously built entry for an Assembly that a
// selective build did not rebuild, so its manifest, frozen capability runtime
// set, and identity are preserved byte-for-byte. ok is false when there is no
// prior build to reuse, in which case the Assembly must be built so the new
// project build record remains complete.
func carryForward(previous *project.BuildRecord, name string) (project.AssemblyBuild, bool) {
	if previous == nil {
		return project.AssemblyBuild{}, false
	}
	return previous.FindAssembly(name)
}

// findDeclaration returns the declaration with the given name.
func findDeclaration(declarations []config.AssemblyDeclaration, name string) (config.AssemblyDeclaration, bool) {
	for _, d := range declarations {
		if d.Name == name {
			return d, true
		}
	}
	return config.AssemblyDeclaration{}, false
}

// relativeManifestPath returns the project-relative manifest path recorded in a
// build entry.
func relativeManifestPath(name string) string {
	return filepath.ToSlash(filepath.Join(manifest.ManifestDir, manifest.AssembliesDir, name, manifest.ManifestFile))
}

// projectFingerprint hashes the whole project's authoring inputs: the config,
// every Assembly entry, and the shared project sources.
func projectFingerprint(root string, cfg config.Config, entries []string) (string, error) {
	if len(entries) == 0 {
		return "", fmt.Errorf("no Assembly entries to fingerprint")
	}
	inputs, err := utils.AuthoringInputs(root, cfg, entries[0])
	if err != nil {
		return "", err
	}
	inputs.Sources = append(inputs.Sources, entries[1:]...)
	fingerprint, err := project.ComputeFingerprint(inputs)
	if err != nil {
		return "", fmt.Errorf("fingerprint project inputs: %w", err)
	}
	return fingerprint, nil
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

// saveBuildRecord persists the build outcome together with the project
// fingerprint that produced it. The record is written immutably to
// .neuron/builds/<fingerprint>/build.json and the current pointer is updated
// atomically to point to the new build.
func saveBuildRecord(root, fingerprint string, assemblies []project.AssemblyBuild) error {
	record := project.BuildRecord{
		Fingerprint: fingerprint,
		BuiltAt:     time.Now().UTC(),
		Assemblies:  assemblies,
	}
	if err := project.SaveBuildRecord(root, fingerprint, record); err != nil {
		return fmt.Errorf("save build record: %w", err)
	}

	// Update the current build pointer atomically.
	if err := project.SaveCurrentPointer(root, fingerprint); err != nil {
		return fmt.Errorf("save current pointer: %w", err)
	}

	return nil
}

// register compiles one Assembly manifest, resolves and freezes its capability
// runtime set and provider identities, and registers it with the Neuron runtime
// through the shared client. It returns the registration response, the frozen
// capability runtime set, and the frozen provider set used to serve
// executions.
func register(ctx context.Context, c *client.Client, cfg config.Config, m *manifest.Assembly, force bool) (*protocol.RegisterResponse, []capabilityrt.ResolvedCapabilityRuntime, []providertypes.ResolvedProvider, error) {
	// Compile the manifest into the runtime core.Assembly representation.
	comp := compiler.New()
	sys, err := comp.Compile(m)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("compile manifest: %w", err)
	}

	// Compute the instance key from the manifest + compiled assembly. The execution
	// environment comes from the effective config, not the manifest.
	key, err := comp.InstanceKey(m, cfg.Runtime.Execution.Mode)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("compute instance key: %w", err)
	}

	configs := buildExecutionConfigurations(cfg, m)

	// Resolve the capability runtime requirements declared by capabilities and freeze the
	// exact dependency set into the register payload, so N.O.R.E. can launch
	// Instances without resolving or installing anything itself.
	resolved, err := resolveFrozenCapabilityRuntimes(ctx, cfg, configs.CapabilityRuntimeRequirements, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	configs.ResolvedCapabilityRuntimes = resolved

	// Freeze the provider identities hosting the frozen capability runtimes'
	// runtime kinds. This is strictly store-only: a normal build never
	// downloads, installs, or silently updates a provider.
	providers, err := freezeProviders(ctx, cfg, resolved)
	if err != nil {
		return nil, nil, nil, err
	}
	configs.ResolvedProviders = providers

	request := protocol.RegisterRequest{
		Key:                     key,
		Assembly:                *sys,
		ExecutionConfigurations: configs,
		Force:                   force,
	}

	result, err := c.Register(ctx, request)
	if err != nil {
		return nil, nil, nil, err
	}

	printRegistration(result)

	return &result, resolved, providers, nil
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

// freezeProviders maps the frozen capability runtime set's runtime kinds to
// their installed provider identities and freezes them for the register
// payload and the build record.
//
// Freezing is strictly store-only: providers are resolved against the
// installed host store and never downloaded, installed, or silently updated
// during a normal build. A kind with no installed host fails with an
// actionable error naming the development bootstrap and install commands.
// Assemblies that use only in-process built-in capability runtimes freeze no
// providers at all.
func freezeProviders(ctx context.Context, cfg config.Config, resolved []capabilityrt.ResolvedCapabilityRuntime) ([]providertypes.ResolvedProvider, error) {
	kinds := make([]string, 0, len(resolved))
	for _, cr := range resolved {
		kinds = append(kinds, cr.Runtime.Type)
	}
	if len(kinds) == 0 {
		return nil, nil
	}

	catalog, err := providerctl.BuildCatalog(providerctl.CatalogConfig{
		ProvidersConfig: cfg.Providers,
	})
	if err != nil {
		return nil, fmt.Errorf("build provider catalog: %w", err)
	}

	providers, err := provider.FreezeForKinds(ctx, catalog.Store, kinds)
	if err != nil {
		return nil, fmt.Errorf("freeze providers: %w", err)
	}
	return providers, nil
}
