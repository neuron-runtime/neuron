package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"

	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
	providertypes "github.com/neuron-runtime/neuron/shared/types/provider"
)

// BootstrapRegistry is the install-record Source marker for artifacts
// installed by development bootstrap rather than resolved from a registry.
// It is persisted in the install record so a bootstrap install is never
// mistaken for a registry install. It is not part of the artifact identity.
const BootstrapRegistry = "bootstrap"

// FirstPartyVersion is the version assigned to descriptor-only first-party
// provider artifacts produced by development bootstrap.
const FirstPartyVersion = "0.1.0"

// FirstParty describes one built-in first-party provider that development
// bootstrap installs into the store.
type FirstParty struct {
	// Name is the provider identity (e.g. "process").
	Name string
	// Kind is the Capability Runtime runtime kind the provider hosts.
	Kind string
	// Platform is the platform key the descriptor-only artifact declares.
	Platform string
}

// FirstPartyProviders returns the first-party providers for the current
// host. The process provider is keyed to the host platform; the WASM
// provider is keyed to the portable wasm32-wasi platform.
func FirstPartyProviders() []FirstParty {
	return []FirstParty{
		{Name: "process", Kind: capabilityrt.RuntimeKindProcess, Platform: runtime.GOOS + "-" + runtime.GOARCH},
		{Name: "wasm", Kind: capabilityrt.RuntimeKindWasm, Platform: capabilityrt.PlatformWasm},
	}
}

// BootstrapResult reports one bootstrapped provider.
type BootstrapResult struct {
	Name           string
	Version        string
	Digest         string
	AlreadyPresent bool
}

// Bootstrap builds descriptor-only first-party provider packages — a valid
// provider.runtime.json with no executable payload — and installs them into
// the store through the real installer. The resulting artifacts are identical
// in layout, identity, and verification semantics to registry installs, so
// project builds and invocations resolve them exactly like installed
// providers.
//
// Bootstrap is a local development mechanism: it performs no registry
// resolution and no download, and the install record marks every artifact
// with the bootstrap source so it is never presented as a registry install.
// It is deterministic and repeatable: identical inputs yield the same content
// digest, and rebuilding over an existing identical artifact reports
// AlreadyPresent instead of failing or duplicating.
func Bootstrap(ctx context.Context, installer *Installer, version string) ([]BootstrapResult, error) {
	if installer == nil {
		return nil, fmt.Errorf("provider installer is nil")
	}
	if version == "" {
		version = FirstPartyVersion
	}
	results := make([]BootstrapResult, 0, 2)
	for _, fp := range FirstPartyProviders() {
		res, err := bootstrapOne(ctx, installer, fp, version)
		if err != nil {
			return nil, err
		}
		results = append(results, *res)
	}
	return results, nil
}

func bootstrapOne(ctx context.Context, installer *Installer, fp FirstParty, version string) (*BootstrapResult, error) {
	m := &providertypes.Manifest{
		APIVersion: providertypes.APIVersion,
		Kind:       providertypes.Kind,
		Metadata: providertypes.ManifestMetadata{
			Name:        fp.Name,
			Version:     version,
			Description: fmt.Sprintf("descriptor-only %s provider bootstrapped for local development", fp.Kind),
		},
		Provider:     providertypes.ManifestProvider{ProtocolVersion: providertypes.ProtocolVersion},
		RuntimeKinds: []string{fp.Kind},
		Protocols:    []string{capabilityrt.ProtocolV1, capabilityrt.ProtocolJSONV1},
		Platforms:    map[string]providertypes.Platform{fp.Platform: {}},
	}
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("bootstrap provider %s: %w", fp.Name, err)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}

	// The installer requires a materializable source; an empty directory
	// plus explicit manifest bytes expresses "descriptor-only" without
	// fabricating a payload.
	stage, err := os.MkdirTemp("", "provider-bootstrap-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)

	res, err := installer.Install(ctx, &Package{
		Name:     fp.Name,
		Version:  version,
		Registry: BootstrapRegistry,
		Manifest: data,
		Source:   stage,
	})
	if err != nil {
		return nil, fmt.Errorf("bootstrap provider %s@%s: %w", fp.Name, version, err)
	}
	return &BootstrapResult{
		Name:           fp.Name,
		Version:        version,
		Digest:         res.Installed.Digest,
		AlreadyPresent: res.AlreadyPresent,
	}, nil
}
