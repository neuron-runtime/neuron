package config

import (
	"encoding/json"
	"fmt"

	"github.com/neuron-runtime/neuron/shared/types/builtins"
)

// BuiltinsConfig bounds the built-in capability runtimes N.O.R.E. provides.
//
// The built-ins are the only capability runtimes that are not installed
// artifacts: they are compiled into the runtime and they reach outside it —
// they open sockets, run programs, and touch the filesystem. The reach they
// are allowed is configured here, per built-in, because a capability that can
// reach the whole machine is not a capability the author controls.
//
// This configuration can only narrow the limits compiled into the runtime. The
// ceilings live in the runtime, not here, so a project configuration file —
// which is part of the assembly's own input — cannot widen what a capability
// can reach.
type BuiltinsConfig struct {
	// Filesystem bounds the filesystem built-in.
	Filesystem BuiltinFilesystemConfig `yaml:"filesystem,omitempty" mapstructure:"filesystem"`

	// Command bounds the command built-in.
	Command BuiltinCommandConfig `yaml:"command,omitempty" mapstructure:"command"`

	// HTTP bounds the http built-in.
	HTTP BuiltinHTTPConfig `yaml:"http,omitempty" mapstructure:"http"`

	// Limits bounds the size of what the built-ins read, write, and return.
	Limits BuiltinLimitsConfig `yaml:"limits,omitempty" mapstructure:"limits"`
}

// BuiltinFilesystemConfig bounds the filesystem built-in's reach.
type BuiltinFilesystemConfig struct {
	// Roots are additional directories the filesystem and command built-ins
	// may reach, resolved against the project root.
	//
	// The project root is always permitted: it is where the assembly lives.
	// These roots extend that reach to the directories the assembly is
	// expected to work with. A path that resolves outside every permitted
	// root is refused.
	Roots []string `yaml:"roots,omitempty" mapstructure:"roots"`
}

// BuiltinCommandConfig bounds the command built-in's reach.
type BuiltinCommandConfig struct {
	// AllowedExecutables restricts the programs the command built-in may run.
	//
	// When it is empty, any program resolvable on the program path may run.
	// When it is not empty, only the listed programs may run, matched by
	// their resolved absolute path.
	AllowedExecutables []string `yaml:"allowedExecutables,omitempty" mapstructure:"allowedExecutables"`
}

// BuiltinHTTPConfig bounds the http built-in's reach.
type BuiltinHTTPConfig struct {
	// AllowedHosts restricts the http built-in to a set of hosts.
	//
	// When it is empty, the built-in may contact any public host and refuses
	// loopback, private, link-local, multicast, unspecified, and cloud
	// metadata addresses. When it is not empty, only the listed hosts may be
	// contacted — including ones that resolve to private addresses, because
	// listing a host is the explicit decision to reach it.
	AllowedHosts []string `yaml:"allowedHosts,omitempty" mapstructure:"allowedHosts"`
}

// BuiltinLimitsConfig bounds the size of what a built-in may read, write, and
// return.
//
// Every limit here is also compiled into the runtime as a ceiling. A value
// above the ceiling is refused rather than clamped, so a configuration that
// asks for more than the runtime allows fails loudly instead of silently
// running under a different limit than the one that was configured.
type BuiltinLimitsConfig struct {
	// MaxCommandOutputBytes bounds the output retained from one stream of a
	// single command.
	MaxCommandOutputBytes int64 `yaml:"maxCommandOutputBytes,omitempty" mapstructure:"maxCommandOutputBytes"`

	// MaxHTTPRequestBodyBytes bounds the request body a single http request
	// may send.
	MaxHTTPRequestBodyBytes int64 `yaml:"maxHTTPRequestBodyBytes,omitempty" mapstructure:"maxHTTPRequestBodyBytes"`

	// MaxHTTPResponseBytes bounds the response body a single http request
	// may read.
	MaxHTTPResponseBytes int64 `yaml:"maxHTTPResponseBytes,omitempty" mapstructure:"maxHTTPResponseBytes"`

	// MaxFileReadBytes bounds the file content a single filesystem read may
	// return.
	MaxFileReadBytes int64 `yaml:"maxFileReadBytes,omitempty" mapstructure:"maxFileReadBytes"`

	// MaxDirectoryEntries bounds the entries a single filesystem listing may
	// return.
	MaxDirectoryEntries int `yaml:"maxDirectoryEntries,omitempty" mapstructure:"maxDirectoryEntries"`

	// DefaultTimeout bounds a single built-in operation when the capability
	// does not set a timeout of its own, as a duration string such as "60s".
	DefaultTimeout string `yaml:"defaultTimeout,omitempty" mapstructure:"defaultTimeout"`
}

// BuiltinPolicy resolves the policy the built-in capability runtimes run
// under from the effective configuration.
//
// The resolution is the shared one, so the configuration can only narrow the
// compiled-in ceilings. A configuration that asks for more than a ceiling
// allows is refused here rather than quietly clamped, because a limit that
// silently differs from the configured one is a limit nobody is looking at.
func (c Config) BuiltinPolicy() (builtins.Policy, error) {
	policy, err := builtins.Effective(builtins.Policy{
		FilesystemRoots:         c.Builtins.Filesystem.Roots,
		AllowedExecutables:      c.Builtins.Command.AllowedExecutables,
		AllowedHTTPHosts:        c.Builtins.HTTP.AllowedHosts,
		MaxCommandOutputBytes:   c.Builtins.Limits.MaxCommandOutputBytes,
		MaxHTTPRequestBodyBytes: c.Builtins.Limits.MaxHTTPRequestBodyBytes,
		MaxHTTPResponseBytes:    c.Builtins.Limits.MaxHTTPResponseBytes,
		MaxFileReadBytes:        c.Builtins.Limits.MaxFileReadBytes,
		MaxDirectoryEntries:     c.Builtins.Limits.MaxDirectoryEntries,
		DefaultTimeout:          c.Builtins.Limits.DefaultTimeout,
	})
	if err != nil {
		return builtins.Policy{}, fmt.Errorf("invalid built-in capability policy: %w", err)
	}
	return policy, nil
}

// resolveBuiltinPaths expands the configured built-in filesystem roots against
// the project root so the runtime compares absolute paths.
func resolveBuiltinPaths(cfg *Config, projectDir string) {
	for i := range cfg.Builtins.Filesystem.Roots {
		cfg.Builtins.Filesystem.Roots[i] = Expand(cfg.Builtins.Filesystem.Roots[i], projectDir)
	}
}

// BuiltinPolicyJSON encodes the effective built-in policy for the runtime's
// command line.
//
// The policy travels as JSON because the runtime is a separate process (and a
// separate Go module): the command line is the transport that already carries
// the project's runtime settings to it.
func BuiltinPolicyJSON(policy builtins.Policy) (string, error) {
	data, err := json.Marshal(policy)
	if err != nil {
		return "", fmt.Errorf("encode built-in capability policy: %w", err)
	}
	return string(data), nil
}
