package config

import (
	"fmt"
	"regexp"
	"strings"
)

// AssemblyDeclaration declares one named Assembly a project defines. A project
// may define several; each is compiled, registered, and served independently
// under its own identity while sharing the project's version and configuration.
type AssemblyDeclaration struct {
	// Name is the Assembly identity. It must be unique within the project and
	// becomes the registered AssemblyID. It is also the directory name of the
	// Assembly's build artifact, so it must be a safe single path segment.
	Name string `yaml:"name,omitempty" mapstructure:"name"`

	// Entry is the Assembly source file, relative to the project root. It must
	// be declared explicitly for a project that lists Assemblies.
	Entry string `yaml:"entry,omitempty" mapstructure:"entry"`

	// Default marks the Assembly `neuron run` selects when no name is given.
	// At most one declaration may be the default.
	Default bool `yaml:"default,omitempty" mapstructure:"default"`
}

// assemblyNamePattern constrains Assembly names to a single, safe path segment.
// The name is used verbatim as a directory under .neuron/assemblies, so it must
// never introduce a path separator or traversal.
var assemblyNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// EffectiveAssemblies returns the ordered Assembly declarations for the project.
//
// A project that declares Assemblies uses them directly. A project that still
// uses the legacy singular `entry` is migrated here to a single declaration
// named after the project (so its registered identity is unchanged), marked as
// the default. The legacy entry is never silently dropped: validateAssemblyDeclarations
// rejects a configuration that carries both forms.
func (c Config) EffectiveAssemblies() []AssemblyDeclaration {
	if len(c.Assemblies) > 0 {
		out := make([]AssemblyDeclaration, len(c.Assemblies))
		copy(out, c.Assemblies)
		return out
	}
	return []AssemblyDeclaration{{
		Name:    c.Name,
		Entry:   c.Entry,
		Default: true,
	}}
}

// DefaultAssembly returns the declaration `neuron run` selects when no Assembly
// name is given. A project with a single Assembly defaults to it; a project with
// several defaults only to one marked `default: true`.
func (c Config) DefaultAssembly() (AssemblyDeclaration, bool) {
	assemblies := c.EffectiveAssemblies()
	if len(assemblies) == 1 {
		return assemblies[0], true
	}
	for _, a := range assemblies {
		if a.Default {
			return a, true
		}
	}
	return AssemblyDeclaration{}, false
}

// validateAssemblyDeclarations enforces the structural rules of the multi-Assembly
// schema. It is called after merging and path resolution so it sees the
// effective configuration, but it never touches the filesystem: whether an
// entry path exists is a build-time concern.
func validateAssemblyDeclarations(c *Config) error {
	if len(c.Assemblies) > 0 && strings.TrimSpace(c.Entry) != "" {
		return fmt.Errorf("configuration declares both `entry` and `assemblies`; `entry` is the legacy single-Assembly form — migrate it into an `assemblies` declaration or remove it")
	}

	seen := make(map[string]bool, len(c.Assemblies))
	defaults := 0
	for i, a := range c.Assemblies {
		name := strings.TrimSpace(a.Name)
		switch {
		case name == "":
			return fmt.Errorf("assemblies[%d]: `name` is required", i)
		case name == "." || name == ".." || !assemblyNamePattern.MatchString(name):
			return fmt.Errorf("assemblies[%d]: invalid Assembly name %q: use letters, digits, '.', '_' or '-' only", i, a.Name)
		case seen[name]:
			return fmt.Errorf("assemblies[%d]: duplicate Assembly name %q", i, name)
		}
		seen[name] = true

		if strings.TrimSpace(a.Entry) == "" {
			return fmt.Errorf("assemblies[%d] (%s): `entry` is required", i, name)
		}
		if a.Default {
			defaults++
		}
	}

	if defaults > 1 {
		return fmt.Errorf("configuration marks %d Assemblies as default; at most one Assembly may be the default", defaults)
	}

	return nil
}
