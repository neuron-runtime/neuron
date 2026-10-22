package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

// Options controls how Load assembles the effective configuration.
type Options struct {
	// ProjectDir is the project root used to locate the project configuration
	// (neuron.config.json/.yaml/.yml) and to resolve relative paths. It may be
	// empty.
	ProjectDir string

	// GlobalPath overrides the global configuration file. When empty the
	// default ~/.config/neuron/config.yaml is used.
	GlobalPath string

	// ProjectPath overrides the project configuration file. When empty it is
	// derived from ProjectDir (neuron.config.json/.yaml/.yml).
	ProjectPath string

	// Environment enables NEURON_* environment-variable overrides.
	Environment bool

	// CLI holds the final command-level overrides keyed by config path, e.g.
	// {"daemon.endpoint": "...", "runtime.execution.mode": "detach"}.
	CLI map[string]any
}

// Load assembles the effective configuration in precedence order:
//
//	Defaults() → global config → project config → environment → CLI
func Load(opts Options) (Config, error) {
	cfg := Defaults()

	v := viper.New()
	registerDefaults(v, cfg)

	// Global user configuration. The global file is also the machine-level
	// surface for runtime defaults, so it is read before the project.
	globalPath := opts.GlobalPath
	if globalPath == "" {
		var err error
		globalPath, err = GlobalConfigPath()
		if err != nil {
			return Config{}, err
		}
	}
	if fileExists(globalPath) {
		if err := validateConfigFile(globalPath, globalScope); err != nil {
			return Config{}, err
		}
		v.SetConfigFile(globalPath)
		if err := v.ReadInConfig(); err != nil {
			return Config{}, fmt.Errorf("read global config: %w", err)
		}
	}

	// Project configuration.
	projectPath := opts.ProjectPath
	var extraCandidates []string
	if projectPath == "" && opts.ProjectDir != "" {
		projectPath, extraCandidates = findProjectConfig(opts.ProjectDir)
		for _, p := range extraCandidates {
			cfg.Warnings = append(cfg.Warnings, fmt.Sprintf(
				"multiple neuron.config.* candidates found; using %s (also found: %s)", projectPath, p,
			))
		}
	}
	if projectPath != "" {
		if !fileExists(projectPath) {
			return Config{}, fmt.Errorf("project config not found: %s", projectPath)
		}
		if isLegacyConfigName(projectPath) {
			return Config{}, fmt.Errorf(
				"project config uses the removed name %s: rename it to neuron.config.json, neuron.config.yaml, or neuron.config.yml",
				filepath.Base(projectPath),
			)
		}
		if err := validateConfigFile(projectPath, projectScope); err != nil {
			return Config{}, err
		}
		v.SetConfigFile(projectPath)
		if err := v.MergeInConfig(); err != nil {
			return Config{}, fmt.Errorf("read project config: %w", err)
		}
		cfg.ConfigFile = projectPath
	} else if opts.ProjectDir != "" {
		// The neuron.yaml / neuron.yml names are reserved for the YAML
		// authoring surface and were removed as config names. Refuse to run
		// with defaults when one sits in the project root.
		if legacy := legacyConfigName(opts.ProjectDir); legacy != "" {
			return Config{}, fmt.Errorf(
				"project config uses the removed name %s: rename it to neuron.config.json, neuron.config.yaml, or neuron.config.yml",
				legacy,
			)
		}
	}

	// Environment variables.
	if opts.Environment {
		v.SetEnvPrefix("NEURON")
		v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
		v.AutomaticEnv()
	}

	// Command-line overrides.
	for key, value := range opts.CLI {
		v.Set(key, value)
	}

	// runtime.port is a union in the configuration file (integer or "auto")
	// and can arrive as a string from the environment. Normalize it once, at
	// this seam, so decoding and every downstream consumer see one type.
	if err := normalizeRuntimePort(v); err != nil {
		return Config{}, err
	}

	if err := v.Unmarshal(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode configuration: %w", err)
	}

	// The project root is the directory that owns the project configuration
	// (neuron.config.*). When --config selects a file, the root is that file's
	// directory; otherwise it is the nearest ancestor of the working directory
	// that holds a project config, falling back to the working directory. All
	// project-relative paths (local capability runtime roots, storage, entry) resolve
	// against this root.
	cfg.ProjectDir = projectRootFor(opts, projectPath)

	resolvePaths(&cfg, cfg.ProjectDir)

	// The built-in policy is resolved here so a configuration that asks for
	// more than the compiled-in ceilings fails when the configuration is
	// loaded, rather than when a capability first reaches for the limit.
	if _, err := cfg.BuiltinPolicy(); err != nil {
		return Config{}, err
	}

	if err := validateAssemblyDeclarations(&cfg); err != nil {
		return Config{}, fmt.Errorf("invalid assemblies: %w", err)
	}

	return cfg, nil
}

// projectRootFor derives the effective project root from the loader options
// and the discovered project-config path.
func projectRootFor(opts Options, projectPath string) string {
	switch {
	case projectPath != "":
		abs, err := filepath.Abs(filepath.Dir(projectPath))
		if err == nil {
			return abs
		}
		return filepath.Dir(projectPath)
	case opts.ProjectPath != "":
		abs, err := filepath.Abs(filepath.Dir(opts.ProjectPath))
		if err == nil {
			return abs
		}
		return filepath.Dir(opts.ProjectPath)
	case opts.ProjectDir != "":
		abs, err := filepath.Abs(opts.ProjectDir)
		if err == nil {
			return abs
		}
		return opts.ProjectDir
	default:
		return ""
	}
}

// registerDefaults seeds every config key with its default so that partial
// overrides from lower layers fall through to the compiled-in value.
func registerDefaults(v *viper.Viper, cfg Config) {
	v.SetDefault("name", cfg.Name)
	v.SetDefault("version", cfg.Version)
	v.SetDefault("lang", cfg.Lang)

	v.SetDefault("runtime.host", cfg.Runtime.Host)
	v.SetDefault("runtime.port", cfg.Runtime.Port)
	v.SetDefault("runtime.execution.mode", cfg.Runtime.Execution.Mode)
	v.SetDefault("runtime.execution.timeout", cfg.Runtime.Execution.Timeout)
	v.SetDefault("runtime.workers.min", cfg.Runtime.Workers.Min)
	v.SetDefault("runtime.workers.max", cfg.Runtime.Workers.Max)
	v.SetDefault("runtime.detachedDrainTimeout", cfg.Runtime.DetachedDrainTimeout)

	v.SetDefault("daemon.endpoint", cfg.Daemon.Endpoint)
	v.SetDefault("daemon.socket", cfg.Daemon.Socket)
	v.SetDefault("daemon.norePath", cfg.Daemon.NorePath)
	v.SetDefault("daemon.pidFile", cfg.Daemon.PIDFile)

	v.SetDefault("storage.provider", cfg.Storage.Provider)
	v.SetDefault("storage.directory", cfg.Storage.Directory)

	v.SetDefault("inspector.enabled", cfg.Inspector.Enabled)
	v.SetDefault("inspector.address", cfg.Inspector.Address)

	if len(cfg.CapabilityRuntimes.Registries) > 0 {
		items := make([]map[string]any, 0, len(cfg.CapabilityRuntimes.Registries))
		for _, reg := range cfg.CapabilityRuntimes.Registries {
			items = append(items, map[string]any{
				"name": reg.Name,
				"url":  reg.URL,
			})
		}
		v.SetDefault("capabilityRuntimes.registries", items)
	}

	v.SetDefault("capabilityRuntimes.storeDir", cfg.CapabilityRuntimes.StoreDir)
	if len(cfg.CapabilityRuntimes.DefaultRegistries) > 0 {
		v.SetDefault("capabilityRuntimes.defaultRegistries", cfg.CapabilityRuntimes.DefaultRegistries)
	}
	if len(cfg.CapabilityRuntimes.LocalRoots) > 0 {
		v.SetDefault("capabilityRuntimes.localRoots", cfg.CapabilityRuntimes.LocalRoots)
	}

	if len(cfg.Providers.Registries) > 0 {
		items := make([]map[string]any, 0, len(cfg.Providers.Registries))
		for _, reg := range cfg.Providers.Registries {
			items = append(items, map[string]any{
				"name": reg.Name,
				"url":  reg.URL,
			})
		}
		v.SetDefault("providers.registries", items)
	}

	v.SetDefault("providers.storeDir", cfg.Providers.StoreDir)
	if len(cfg.Providers.DefaultRegistries) > 0 {
		v.SetDefault("providers.defaultRegistries", cfg.Providers.DefaultRegistries)
	}
	if len(cfg.Providers.LocalRoots) > 0 {
		v.SetDefault("providers.localRoots", cfg.Providers.LocalRoots)
	}
	v.SetDefault("dev.maxWorkers", cfg.Dev.MaxWorkers)
	v.SetDefault("entry", cfg.Entry)
	v.SetDefault("variables", cfg.Variables)

	v.SetDefault("builtins.filesystem.roots", cfg.Builtins.Filesystem.Roots)
	v.SetDefault("builtins.command.allowedExecutables", cfg.Builtins.Command.AllowedExecutables)
	v.SetDefault("builtins.http.allowedHosts", cfg.Builtins.HTTP.AllowedHosts)
	v.SetDefault("builtins.limits.maxCommandOutputBytes", cfg.Builtins.Limits.MaxCommandOutputBytes)
	v.SetDefault("builtins.limits.maxHTTPRequestBodyBytes", cfg.Builtins.Limits.MaxHTTPRequestBodyBytes)
	v.SetDefault("builtins.limits.maxHTTPResponseBytes", cfg.Builtins.Limits.MaxHTTPResponseBytes)
	v.SetDefault("builtins.limits.maxFileReadBytes", cfg.Builtins.Limits.MaxFileReadBytes)
	v.SetDefault("builtins.limits.maxDirectoryEntries", cfg.Builtins.Limits.MaxDirectoryEntries)
	v.SetDefault("builtins.limits.defaultTimeout", cfg.Builtins.Limits.DefaultTimeout)
}

// resolvePaths expands any relative or "~"-prefixed path fields against the
// project root so downstream consumers always receive absolute paths.
func resolvePaths(cfg *Config, projectDir string) {
	cfg.Storage.Directory = Expand(cfg.Storage.Directory, projectDir)
	cfg.Daemon.Socket = Expand(cfg.Daemon.Socket, projectDir)
	cfg.Daemon.PIDFile = Expand(cfg.Daemon.PIDFile, projectDir)
	cfg.CapabilityRuntimes.StoreDir = Expand(cfg.CapabilityRuntimes.StoreDir, projectDir)
	cfg.Providers.StoreDir = Expand(cfg.Providers.StoreDir, projectDir)
	cfg.Entry = Expand(cfg.Entry, projectDir)
	for i := range cfg.Assemblies {
		if cfg.Assemblies[i].Entry != "" {
			cfg.Assemblies[i].Entry = Expand(cfg.Assemblies[i].Entry, projectDir)
		}
	}

	if cfg.Daemon.NorePath != "" {
		cfg.Daemon.NorePath = Expand(cfg.Daemon.NorePath, projectDir)
	}

	for i := range cfg.CapabilityRuntimes.LocalRoots {
		cfg.CapabilityRuntimes.LocalRoots[i] = Expand(cfg.CapabilityRuntimes.LocalRoots[i], projectDir)
	}

	for i := range cfg.Providers.LocalRoots {
		cfg.Providers.LocalRoots[i] = Expand(cfg.Providers.LocalRoots[i], projectDir)
	}

	resolveBuiltinPaths(cfg, projectDir)

	for i := range cfg.CapabilityRuntimes.Registries {
		reg := &cfg.CapabilityRuntimes.Registries[i]
		if reg.Name == "local" && reg.URL != "" && reg.URL != "local://" {
			reg.URL = Expand(reg.URL, projectDir)
		}
	}
}

// findProjectConfig locates the project configuration file by its modern name,
// walking upward from projectDir until it finds one or reaches the user's home
// directory or the fileassembly root. The legacy neuron.yaml/neuron.yml names
// are deliberately not accepted. It returns the chosen file and any other
// candidates found in the same directory so callers can warn about ambiguity.
func findProjectConfig(projectDir string) (string, []string) {
	dir, err := filepath.Abs(projectDir)
	if err != nil {
		dir = projectDir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	if home != "" {
		home, _ = filepath.Abs(home)
	}

	var others []string
	for {
		found := ""
		for _, name := range []string{"neuron.config.json", "neuron.config.yaml", "neuron.config.yml"} {
			p := filepath.Join(dir, name)
			if fileExists(p) {
				if found == "" {
					found = p
				} else {
					others = append(others, p)
				}
			}
		}
		if found != "" {
			return found, others
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", others
		}
		if home != "" && filepath.Clean(dir) == filepath.Clean(home) {
			return "", others
		}
		dir = parent
	}
}

// legacyConfigName returns the removed legacy project-config name present in
// projectDir, or the empty string.
func legacyConfigName(projectDir string) string {
	for _, name := range []string{"neuron.yaml", "neuron.yml"} {
		p := filepath.Join(projectDir, name)
		if fileExists(p) {
			return name
		}
	}
	return ""
}

// isLegacyConfigName reports whether path uses a removed legacy config name.
func isLegacyConfigName(path string) bool {
	switch filepath.Base(path) {
	case "neuron.yaml", "neuron.yml":
		return true
	default:
		return false
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
