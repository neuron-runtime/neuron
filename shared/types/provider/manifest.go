// Package provider defines the artifact manifest schema for installable
// providers (provider.runtime.json). The structure mirrors the
// capabilityruntime.Manifest but is scoped to provider artifacts: identity,
// protocol compatibility, which runtime kinds a provider can host, which
// capability runtime invocation protocols it carries, and platform artifacts.
package provider

import (
	"fmt"
	"strings"
)

// Provider manifest constants.
const (
	// APIVersion is the schema version of every provider.runtime.json manifest.
	APIVersion = "neuron/provider/v1"

	// Kind is the discriminant of every provider.runtime.json manifest.
	Kind = "ProviderRuntime"

	// ManifestFile is the mandatory manifest file inside every provider
	// artifact and every installed provider directory.
	ManifestFile = "provider.runtime.json"

	// InstallFile is the installation record written into an installed
	// provider directory after a verified, atomic install.
	InstallFile = "install.json"

	// PackageArchiveSuffix is the canonical suffix for provider package
	// archives: <name>-<version>-provider.neuron.tar.gz
	PackageArchiveSuffix = "-provider.neuron.tar.gz"
)

// PackageArchiveName returns the canonical name of the provider package
// archive for a provider name and version.
func PackageArchiveName(name, version string) string {
	clean := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(name), ":", "-"), "/", "-")
	return clean + "-" + strings.TrimSpace(version) + PackageArchiveSuffix
}

// Manifest is the provider.runtime.json manifest.
type Manifest struct {
	APIVersion   string              `json:"apiVersion"`
	Kind         string              `json:"kind"`
	Metadata     ManifestMetadata    `json:"metadata"`
	Provider     ManifestProvider    `json:"provider"`
	RuntimeKinds []string            `json:"runtimeKinds"`
	Protocols    []string            `json:"protocols,omitempty"`
	Platforms    map[string]Platform `json:"platforms"`
	Build        *ManifestBuild      `json:"build,omitempty"`
	Artifact     *ManifestArtifact   `json:"artifact,omitempty"`
}

// ManifestMetadata identifies the provider artifact.
type ManifestMetadata struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
}

// ManifestProvider describes provider contract details.
type ManifestProvider struct {
	ProtocolVersion string `json:"protocolVersion,omitempty"`
}

// ManifestBuild is optional authoring-time build instruction.
type ManifestBuild struct {
	Command string `json:"command,omitempty"`
}

// ManifestArtifact locates the built payload relative to the package dir.
type ManifestArtifact struct {
	Path string `json:"path,omitempty"`
}

// Platform maps a platform key to its artifact and checksum.
type Platform struct {
	Artifact string `json:"artifact"`
	SHA256   string `json:"sha256,omitempty"`
}

// Validate verifies the provider manifest conforms to its schema.
func (m *Manifest) Validate() error {
	if m == nil {
		return fmt.Errorf("provider manifest is nil")
	}
	if m.APIVersion != APIVersion {
		return fmt.Errorf("provider manifest: unsupported apiVersion %q (want %q)", m.APIVersion, APIVersion)
	}
	if m.Kind != Kind {
		return fmt.Errorf("provider manifest: unsupported kind %q (want %q)", m.Kind, Kind)
	}
	if m.Metadata.Name == "" {
		return fmt.Errorf("provider manifest: metadata.name is required")
	}
	if m.Metadata.Version == "" {
		return fmt.Errorf("provider manifest: metadata.version is required")
	}
	if len(m.RuntimeKinds) == 0 {
		return fmt.Errorf("provider manifest %s: runtimeKinds is required", m.Metadata.Name)
	}
	if m.Provider.ProtocolVersion != "" && m.Provider.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("provider manifest %s: incompatible protocolVersion %q (want %q)", m.Metadata.Name, m.Provider.ProtocolVersion, ProtocolVersion)
	}
	return nil
}

// Sanitized returns a copy safe to persist in installed provider dirs.
func (m *Manifest) Sanitized() *Manifest {
	if m == nil {
		return nil
	}
	clone := *m
	clone.Build = nil
	clone.Artifact = nil
	if clone.Platforms != nil {
		pc := make(map[string]Platform, len(clone.Platforms))
		for k, v := range clone.Platforms {
			pc[k] = v
		}
		clone.Platforms = pc
	}
	return &clone
}

// ToDescriptor converts the manifest to a provider Descriptor.
func (m *Manifest) ToDescriptor() Descriptor {
	proto := m.Provider.ProtocolVersion
	if proto == "" {
		proto = ProtocolVersion
	}
	return Descriptor{
		Name:                       m.Metadata.Name,
		Version:                    m.Metadata.Version,
		ProtocolVersion:            proto,
		RuntimeKinds:               append([]string(nil), m.RuntimeKinds...),
		CapabilityRuntimeProtocols: append([]string(nil), m.Protocols...),
	}
}
