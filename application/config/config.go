// Package config produces the effective Neuron runtime configuration.
//
// Configuration is assembled from several layers, each overriding the one
// below it:
//
//  1. built-in defaults
//  2. global user configuration  (~/.config/neuron/config.yaml)
//  3. project configuration       (neuron.config.json | neuron.config.yaml | neuron.config.yml)
//  4. environment variables       (NEURON_*)
//  5. command-line overrides      (Options.CLI)
//
// The rest of Neuron never touches Viper or the YAML representation. It only
// sees this typed Config, which is why the loader is the only file in this
// package that depends on Viper.
package config

import (
	"os"
	"path/filepath"
)

// Config is the effective Neuron configuration.
type Config struct {
	Version int `yaml:"version,omitempty" mapstructure:"version"`

	// Lang is the canonical authoring language. It defaults to "typescript".
	Lang string `yaml:"lang,omitempty" mapstructure:"lang"`

	// Entry is the system source file relative to the project root
	// (e.g. "system.ts" or "system.yaml"). When empty the default entry for
	// the resolved language is used.
	Entry string `yaml:"entry,omitempty" mapstructure:"entry"`

	// Variables are project-level values passed into the compiled system
	// manifest. They have no meaning to the runtime itself.
	Variables map[string]any `yaml:"variables,omitempty" mapstructure:"variables"`

	Runtime   RuntimeConfig   `yaml:"runtime" mapstructure:"runtime"`
	Daemon    DaemonConfig    `yaml:"daemon" mapstructure:"daemon"`
	Storage   StorageConfig   `yaml:"storage" mapstructure:"storage"`
	Executors ExecutorsConfig `yaml:"executors" mapstructure:"executors"`
	Inspector InspectorConfig `yaml:"inspector" mapstructure:"inspector"`

	// Dev controls developer-experience options.
	Dev DevConfig `yaml:"dev,omitempty" mapstructure:"dev"`

	// ProjectDir is the absolute path of the project root: the directory that
	// owns the project configuration (neuron.config.*). It is computed during
	// Load and never read from a configuration file. Downstream consumers use
	// it to resolve project-relative paths (implicit executor roots, build
	// artifacts).
	ProjectDir string `yaml:"-" mapstructure:"-"`

	// Warnings surfaces non-fatal configuration notices (for example multiple
	// neuron.config.* candidates found during discovery). It is never loaded
	// from a configuration file.
	Warnings []string `yaml:"-" mapstructure:"-"`
}

// RuntimeConfig holds N.O.R.E.-related runtime defaults.
type RuntimeConfig struct {
	Execution ExecutionConfig `yaml:"execution" mapstructure:"execution"`
	Workers   WorkerConfig    `yaml:"workers"   mapstructure:"workers"`
}

// ExecutionConfig controls how executions behave by default.
type ExecutionConfig struct {
	// Mode is "wait" or "detach".
	Mode string `yaml:"mode" mapstructure:"mode"`

	// Timeout is a duration string such as "30m". Kept as a string so it
	// unmarshals from YAML without custom parsing.
	Timeout string `yaml:"timeout" mapstructure:"timeout"`
}

// WorkerConfig describes the executor worker pool.
type WorkerConfig struct {
	Min int `yaml:"min,omitempty" mapstructure:"min"`
	Max int `yaml:"max,omitempty" mapstructure:"max"`
}

// DaemonConfig controls the local N.O.R.E. daemon.
type DaemonConfig struct {
	// Endpoint, when non-empty, selects a remote N.O.R.E. over its
	// daemon/socket. An empty value means the local Unix socket daemon.
	Endpoint string `yaml:"endpoint,omitempty" mapstructure:"endpoint"`

	// Socket is the local Unix socket the daemon listens on.
	Socket string `yaml:"socket,omitempty" mapstructure:"socket"`

	// NorePath overrides the daemon binary path.
	NorePath string `yaml:"norePath,omitempty" mapstructure:"norePath"`

	// PIDFile is where the daemon records its process id.
	PIDFile string `yaml:"pidFile,omitempty" mapstructure:"pidFile"`
}

// StorageConfig selects the storage provider and its root directory.
//
// Storage is internal: Neuron manages it and it cannot be set from a
// configuration file. It remains on Config only so the daemon bootstrap can
// derive its data directory and the CLI can test programmatically.
type StorageConfig struct {
	Provider string `yaml:"provider" mapstructure:"provider"`

	// Directory is the provider's data directory. The storage implementation
	// decides the individual subdirectories (systems, instances, ...).
	Directory string `yaml:"directory" mapstructure:"directory"`
}

// ExecutorsConfig lists the executable registries Neuron can resolve services
// against. A service only declares `type`/`version`; the registry resolution
// system determines where the executor comes from.
type ExecutorsConfig struct {
	// Registries lists the registry providers Neuron can resolve executors
	// against.
	Registries []ExecutorRegistry `yaml:"registries,omitempty" mapstructure:"registries"`

	// LocalRoots lists additional project-local executor search roots,
	// resolved against the project root. The project's own ./neuron/executors
	// directory is always an implicit local root and does not need to be
	// listed here.
	LocalRoots []string `yaml:"localRoots,omitempty" mapstructure:"localRoots"`

	// StoreDir is the local installed-executor directory. Defaults to
	// ~/.neuron/executors. Like storage, it is internal and rejected from
	// configuration files.
	StoreDir string `yaml:"storeDir,omitempty" mapstructure:"storeDir"`

	// DefaultRegistries is the ordered list of registry names a Requirement
	// uses when it declares no registries of its own. Defaults to ["local"].
	DefaultRegistries []string `yaml:"defaultRegistries,omitempty" mapstructure:"defaultRegistries"`
}

// DefaultStoreDir returns the store directory used when none is configured.
func DefaultStoreDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".neuron/executors"
	}
	return filepath.Join(home, ".neuron", "executors")
}

// ExecutorRegistry identifies a source of executor definitions.
type ExecutorRegistry struct {
	Name string `yaml:"name,omitempty" mapstructure:"name"`
	URL  string `yaml:"url"            mapstructure:"url"`
}

// InspectorConfig controls the inspector endpoint.
type InspectorConfig struct {
	Enabled bool   `yaml:"enabled,omitempty" mapstructure:"enabled"`
	Address string `yaml:"address,omitempty" mapstructure:"address"`
}

// DevConfig carries developer-experience options. Everything here has a
// sensible default and is optional to author.
type DevConfig struct {
	// MaxWorkers bounds how many local executor build commands may run at
	// once during `neuron build`. Defaults to 1 (sequential builds).
	MaxWorkers int `yaml:"maxWorkers,omitempty" mapstructure:"maxWorkers"`
}
