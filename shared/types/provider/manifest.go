package provider

import (
	"fmt"
	"sort"
	"strings"

	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
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
	return sanitizeName(name) + "-" + strings.TrimSpace(version) + PackageArchiveSuffix
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

// knownRuntimeKinds is the closed set of runtime kinds a provider may declare.
// It includes the reserved container and remote kinds so a provider built for a
// future backend is a valid manifest on a build that cannot host it yet; the
// host's ability is checked separately at registry selection time.
func knownRuntimeKinds() map[string]struct{} {
	return map[string]struct{}{
		capabilityrt.RuntimeKindProcess:   {},
		capabilityrt.RuntimeKindWasm:      {},
		capabilityrt.RuntimeKindContainer: {},
		capabilityrt.RuntimeKindRemote:    {},
	}
}

// knownProtocols is the set of Capability Runtime invocation protocols a
// provider artifact may declare it carries.
func knownProtocols() map[string]struct{} {
	return map[string]struct{}{
		capabilityrt.ProtocolV1:     {},
		capabilityrt.ProtocolJSONV1: {},
	}
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
	kinds := knownRuntimeKinds()
	for _, kind := range m.RuntimeKinds {
		if _, ok := kinds[kind]; !ok {
			return fmt.Errorf("provider manifest %s: unknown runtimeKind %q", m.Metadata.Name, kind)
		}
	}
	protocols := knownProtocols()
	for _, proto := range m.Protocols {
		if _, ok := protocols[proto]; !ok {
			return fmt.Errorf("provider manifest %s: unknown protocol %q", m.Metadata.Name, proto)
		}
	}
	if len(m.Platforms) == 0 {
		return fmt.Errorf("provider manifest %s: platforms is required", m.Metadata.Name)
	}
	for platform := range m.Platforms {
		if strings.TrimSpace(platform) == "" {
			return fmt.Errorf("provider manifest %s: platform key is empty", m.Metadata.Name)
		}
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

// ToDescriptor converts the manifest to a provider Descriptor. The platform
// keys are carried through in sorted order so the descriptor is deterministic
// and a caller can compare descriptors without map-ordering noise.
func (m *Manifest) ToDescriptor() Descriptor {
	proto := m.Provider.ProtocolVersion
	if proto == "" {
		proto = ProtocolVersion
	}
	platforms := make([]string, 0, len(m.Platforms))
	for platform := range m.Platforms {
		platforms = append(platforms, platform)
	}
	sort.Strings(platforms)
	return Descriptor{
		Name:                       m.Metadata.Name,
		Version:                    m.Metadata.Version,
		ProtocolVersion:            proto,
		RuntimeKinds:               append([]string(nil), m.RuntimeKinds...),
		CapabilityRuntimeProtocols: append([]string(nil), m.Protocols...),
		Platforms:                  platforms,
	}
}
