package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"github.com/Muhammad-Jay/neuron/shared/types/core"
)

// HashAssembly returns a deterministic content hash for an Assembly.
//
// The parser generates random IDs (assembly and binding metadata IDs) on
// every parse, so the raw struct cannot be hashed directly. HashAssembly
// normalizes the assembly first: generated IDs are dropped, capabilities and
// bindings are sorted, and map marshaling (key-sorted by encoding/json)
// stays stable.
func HashAssembly(assembly core.Assembly) (string, error) {
	data, err := json.Marshal(normalizeAssembly(assembly))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// AssemblyKey derives the identity of an Assembly from its metadata. Hash is
// computed deterministically via HashAssembly.
func AssemblyKey(assembly core.Assembly, env string) (InstanceKey, error) {
	hash, err := HashAssembly(assembly)
	if err != nil {
		return InstanceKey{}, err
	}
	id := assembly.Metadata.Name
	if id == "" {
		id = "assembly"
	}
	version := assembly.Metadata.Version
	if version == "" {
		version = "latest"
	}
	if env == "" {
		env = "development"
	}
	return InstanceKey{AssemblyID: id, Version: version, Hash: hash, Env: env}, nil
}

type normalizedAssembly struct {
	Metadata     normalizedMetadata     `json:"metadata"`
	Capabilities []normalizedCapability `json:"capabilities"`
	Bindings     []normalizedBinding    `json:"bindings"`
}

type normalizedMetadata struct {
	Name        string            `json:"name"`
	Version     string            `json:"version,omitempty"`
	Description string            `json:"description,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

type normalizedCapability struct {
	ID          string                     `json:"id"`
	Type        core.CapabilityRuntimeType `json:"type"`
	Config      map[string]any             `json:"config"`
	Params      []core.Port                `json:"params,omitempty"`
	Results     []core.Port                `json:"results,omitempty"`
	Timeout     string                     `json:"timeout,omitempty"`
	MaxAttempts int                        `json:"max_attempts,omitempty"`
	Backoff     string                     `json:"backoff,omitempty"`
}

type normalizedBinding struct {
	From        string                `json:"from"`
	To          string                `json:"to"`
	Mappings    []core.MappingRule    `json:"mappings,omitempty"`
	Validations []core.ValidationRule `json:"validations,omitempty"`
}

func normalizeAssembly(assembly core.Assembly) normalizedAssembly {
	m := assembly.Metadata
	capabilities := make([]normalizedCapability, 0, len(assembly.Specification.Capabilities)+len(assembly.Specification.Triggers))
	for _, trigger := range assembly.Specification.Triggers {
		capabilities = append(capabilities, normalizedCapability{
			ID:          string(trigger.Metadata.ID),
			Type:        trigger.Type,
			Config:      trigger.CapabilityConfigurations,
			Params:      append([]core.Port(nil), trigger.Params...),
			Results:     append([]core.Port(nil), trigger.Results...),
			Timeout:     trigger.RuntimeConfigurations.Timeout,
			MaxAttempts: trigger.RuntimeConfigurations.Retry.MaxAttempts,
			Backoff:     trigger.RuntimeConfigurations.Retry.Backoff,
		})
	}
	for _, cap := range assembly.Specification.Capabilities {
		capabilities = append(capabilities, normalizedCapability{
			ID:          string(cap.Metadata.ID),
			Type:        cap.Type,
			Config:      cap.CapabilityConfigurations,
			Params:      append([]core.Port(nil), cap.Params...),
			Results:     append([]core.Port(nil), cap.Results...),
			Timeout:     cap.RuntimeConfigurations.Timeout,
			MaxAttempts: cap.RuntimeConfigurations.Retry.MaxAttempts,
			Backoff:     cap.RuntimeConfigurations.Retry.Backoff,
		})
	}
	sort.Slice(capabilities, func(i, j int) bool { return capabilities[i].ID < capabilities[j].ID })

	bindings := make([]normalizedBinding, 0, len(assembly.Specification.Bindings))
	for _, b := range assembly.Specification.Bindings {
		mappings := append([]core.MappingRule(nil), b.Mappings...)
		sort.Slice(mappings, func(i, j int) bool { return mappings[i].TargetPath < mappings[j].TargetPath })
		validations := append([]core.ValidationRule(nil), b.Validations...)
		sort.Slice(validations, func(i, j int) bool { return validations[i].Expression < validations[j].Expression })
		bindings = append(bindings, normalizedBinding{
			From:        string(b.From.CapabilityID),
			To:          string(b.To.CapabilityID),
			Mappings:    mappings,
			Validations: validations,
		})
	}
	sort.Slice(bindings, func(i, j int) bool {
		if bindings[i].From != bindings[j].From {
			return bindings[i].From < bindings[j].From
		}
		return bindings[i].To < bindings[j].To
	})

	return normalizedAssembly{
		Metadata: normalizedMetadata{
			Name:        m.Name,
			Version:     m.Version,
			Description: m.Description,
			Labels:      m.Labels,
		},
		Capabilities: capabilities,
		Bindings:     bindings,
	}
}
