package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
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
		if err := rejectInternalKeys(globalPath); err != nil {
			return Config{}, err
		}
		v.SetConfigFile(globalPath)
		if err := v.ReadInConfig(); err != nil {
			return Config{}, fmt.Errorf("read global config: %w", err)
		}
	}

	// Project configuration.
	projectPath := opts.ProjectPath
	if projectPath == "" && opts.ProjectDir != "" {
		projectPath = findProjectConfig(opts.ProjectDir)
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
		if err := rejectInternalKeys(projectPath); err != nil {
			return Config{}, err
		}
		if err := rejectInternalKeys(projectPath); err != nil {
			return Config{}, err
		}
		v.SetConfigFile(projectPath)
		if err := v.MergeInConfig(); err != nil {
			return Config{}, fmt.Errorf("read project config: %w", err)
		}
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

	if err := v.Unmarshal(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode configuration: %w", err)
	}

	resolvePaths(&cfg, opts.ProjectDir)

	return cfg, nil
}

// registerDefaults seeds every config key with its default so that partial
// overrides from lower layers fall through to the compiled-in value.
func registerDefaults(v *viper.Viper, cfg Config) {
	v.SetDefault("version", cfg.Version)
	v.SetDefault("lang", cfg.Lang)

	v.SetDefault("runtime.execution.mode", cfg.Runtime.Execution.Mode)
	v.SetDefault("runtime.execution.timeout", cfg.Runtime.Execution.Timeout)
	v.SetDefault("runtime.workers.min", cfg.Runtime.Workers.Min)
	v.SetDefault("runtime.workers.max", cfg.Runtime.Workers.Max)

	v.SetDefault("daemon.endpoint", cfg.Daemon.Endpoint)
	v.SetDefault("daemon.socket", cfg.Daemon.Socket)
	v.SetDefault("daemon.norePath", cfg.Daemon.NorePath)
	v.SetDefault("daemon.pidFile", cfg.Daemon.PIDFile)

	v.SetDefault("storage.provider", cfg.Storage.Provider)
	v.SetDefault("storage.directory", cfg.Storage.Directory)

	v.SetDefault("inspector.enabled", cfg.Inspector.Enabled)
	v.SetDefault("inspector.address", cfg.Inspector.Address)

	if len(cfg.Executors.Registries) > 0 {
		items := make([]map[string]any, 0, len(cfg.Executors.Registries))
		for _, reg := range cfg.Executors.Registries {
			items = append(items, map[string]any{
				"name": reg.Name,
				"url":  reg.URL,
			})
		}
		v.SetDefault("executors.registries", items)
	}

	v.SetDefault("executors.storeDir", cfg.Executors.StoreDir)
	if len(cfg.Executors.DefaultRegistries) > 0 {
		v.SetDefault("executors.defaultRegistries", cfg.Executors.DefaultRegistries)
	}
	v.SetDefault("entry", cfg.Entry)
	v.SetDefault("variables", cfg.Variables)
}

// resolvePaths expands any relative or "~"-prefixed path fields against the
// project root so downstream consumers always receive absolute paths.
func resolvePaths(cfg *Config, projectDir string) {
	cfg.Storage.Directory = Expand(cfg.Storage.Directory, projectDir)
	cfg.Daemon.Socket = Expand(cfg.Daemon.Socket, projectDir)
	cfg.Daemon.PIDFile = Expand(cfg.Daemon.PIDFile, projectDir)
	cfg.Executors.StoreDir = Expand(cfg.Executors.StoreDir, projectDir)
	cfg.Entry = Expand(cfg.Entry, projectDir)

	if cfg.Daemon.NorePath != "" {
		cfg.Daemon.NorePath = Expand(cfg.Daemon.NorePath, projectDir)
	}
}

// findProjectConfig locates the project configuration file by its modern name.
// The legacy neuron.yaml/neuron.yml names are deliberately not accepted.
func findProjectConfig(projectDir string) string {
	for _, name := range []string{"neuron.config.json", "neuron.config.yaml", "neuron.config.yml"} {
		p := filepath.Join(projectDir, name)
		if fileExists(p) {
			return p
		}
	}
	return ""
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

// rejectInternalKeys rejects configuration-file keys that Neuron owns
// internally and that must not be authored by users. Managing them in a config
// file is how broken relative paths and unsupported providers leak in.
func rejectInternalKeys(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	var root map[string]any
	if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("decode config: %w", err)
	}

	if _, ok := root["storage"]; ok {
		return fmt.Errorf("config %s: `storage` is managed by Neuron and cannot be configured here", filepath.Base(path))
	}

	if executors, ok := root["executors"].(map[string]any); ok {
		if _, ok := executors["storeDir"]; ok {
			return fmt.Errorf("config %s: `executors.storeDir` is managed by Neuron and cannot be configured here", filepath.Base(path))
		}
	}

	return nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
