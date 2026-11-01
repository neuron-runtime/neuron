package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"github.com/neuron-runtime/neuron/shared/types/core"
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
	// Params and Result are the Assembly's public contract. Params keep the
	// author's declared order; the Result contributes its Schema in declared
	// order and its Fields in declared order, each with its deterministic
	// structured source. Neither is sorted: the order is the contract.
	Params []core.Port               `json:"params,omitempty"`
	Result *normalizedAssemblyResult `json:"result,omitempty"`
}

type normalizedMetadata struct {
	Name    string            `json:"name"`
	Version string            `json:"version,omitempty"`
	Labels  map[string]string `json:"labels,omitempty"`
}

type normalizedAssemblyResult struct {
	Schema []core.Port                     `json:"schema"`
	Fields []normalizedAssemblyResultField `json:"fields"`
}

type normalizedAssemblyResultField struct {
	Name   string         `json:"name"`
	Source *core.ValueRef `json:"source"`
}

type normalizedCapability struct {
	ID      string                     `json:"id"`
	Type    core.CapabilityRuntimeType `json:"type"`
	Config  map[string]any             `json:"config"`
	Params  []core.Port                `json:"params,omitempty"`
	Results []core.Port                `json:"results,omitempty"`
	// RuntimeConfig is the capability's declared runtime configuration, which
	// is part of the assembly's deployment identity. N.O.R.E.'s own defaults
	// are deliberately excluded: they are applied after registration and are
	// never part of what an author deployed, so they must not invalidate an
	// existing registration when the runtime engine is upgraded. A runtimeConfig
	// that declares nothing is treated as no runtimeConfig, since both resolve
	// to the same defaults.
	RuntimeConfig *core.RuntimeConfig `json:"runtimeConfig,omitempty"`
}

// declaredRuntimeConfig drops a runtimeConfig that carries no declaration, so
// that "declared an empty group" and "declared nothing" contribute identically
// to the hash.
func declaredRuntimeConfig(config *core.RuntimeConfig) *core.RuntimeConfig {
	if config.IsEmpty() {
		return nil
	}
	return config
}

type normalizedBinding struct {
	From string `json:"from"`
	To   string `json:"to"`
	// JoinPolicy is normalized to its effective value so an omitted policy and
	// an explicit JoinPolicyAll contribute identically: they mean the same thing
	// at execution time and must not invalidate an otherwise identical assembly.
	JoinPolicy  core.JoinPolicy       `json:"joinPolicy"`
	Mappings    []core.MappingRule    `json:"mappings,omitempty"`
	Validations []core.ValidationRule `json:"validations,omitempty"`
}

// bindingSortKey renders a normalized binding in its deterministic JSON form.
// encoding/json sorts map keys, so the key is stable for every field the hash
// includes, including structured mapping sources and inline literal values.
func bindingSortKey(binding normalizedBinding) string {
	data, err := json.Marshal(binding)
	if err != nil {
		return binding.From + "\x00" + binding.To + "\x00" + string(binding.JoinPolicy)
	}
	return string(data)
}

// mappingSortKey is the deterministic tie-breaker for two mappings that target
// the same path.
func mappingSortKey(mapping core.MappingRule) string {
	data, err := json.Marshal(mapping)
	if err != nil {
		return mapping.TargetPath
	}
	return string(data)
}

func normalizeAssembly(assembly core.Assembly) normalizedAssembly {
	m := assembly.Metadata
	capabilities := make([]normalizedCapability, 0, len(assembly.Specification.Capabilities)+len(assembly.Specification.Triggers))
	for _, trigger := range assembly.Specification.Triggers {
		capabilities = append(capabilities, normalizedCapability{
			ID:            string(trigger.Metadata.ID),
			Type:          trigger.Type,
			Config:        trigger.CapabilityConfigurations,
			Params:        append([]core.Port(nil), trigger.Params...),
			Results:       append([]core.Port(nil), trigger.Results...),
			RuntimeConfig: declaredRuntimeConfig(trigger.RuntimeConfig),
		})
	}
	for _, cap := range assembly.Specification.Capabilities {
		capabilities = append(capabilities, normalizedCapability{
			ID:            string(cap.Metadata.ID),
			Type:          cap.Type,
			Config:        cap.CapabilityConfigurations,
			Params:        append([]core.Port(nil), cap.Params...),
			Results:       append([]core.Port(nil), cap.Results...),
			RuntimeConfig: declaredRuntimeConfig(cap.RuntimeConfig),
		})
	}
	sort.Slice(capabilities, func(i, j int) bool { return capabilities[i].ID < capabilities[j].ID })

	bindings := make([]normalizedBinding, 0, len(assembly.Specification.Bindings))
	for _, b := range assembly.Specification.Bindings {
		mappings := append([]core.MappingRule(nil), b.Mappings...)
		sort.Slice(mappings, func(i, j int) bool {
			if mappings[i].TargetPath != mappings[j].TargetPath {
				return mappings[i].TargetPath < mappings[j].TargetPath
			}
			// Two rules may share a target path in an artifact the compiler did
			// not produce. Their structured source is the tie-breaker so the
			// hash never depends on the order they happened to be declared in.
			return mappingSortKey(mappings[i]) < mappingSortKey(mappings[j])
		})
		validations := append([]core.ValidationRule(nil), b.Validations...)
		sort.Slice(validations, func(i, j int) bool {
			if validations[i].Expression != validations[j].Expression {
				return validations[i].Expression < validations[j].Expression
			}
			return validations[i].Message < validations[j].Message
		})
		bindings = append(bindings, normalizedBinding{
			From:        string(b.From.CapabilityID),
			To:          string(b.To.CapabilityID),
			JoinPolicy:  b.JoinPolicy.Effective(),
			Mappings:    mappings,
			Validations: validations,
		})
	}
	// Bindings are sorted by identity and then by every remaining contributing
	// field. From/To alone is not a total order: parallel edges may share both
	// endpoints while differing in mappings, validations, or join policy, and
	// their relative order must not depend on declaration order.
	sort.Slice(bindings, func(i, j int) bool {
		if bindings[i].From != bindings[j].From {
			return bindings[i].From < bindings[j].From
		}
		if bindings[i].To != bindings[j].To {
			return bindings[i].To < bindings[j].To
		}
		if bindings[i].JoinPolicy != bindings[j].JoinPolicy {
			return bindings[i].JoinPolicy < bindings[j].JoinPolicy
		}
		return bindingSortKey(bindings[i]) < bindingSortKey(bindings[j])
	})

	return normalizedAssembly{
		Metadata: normalizedMetadata{
			Name:    m.Name,
			Version: m.Version,
			Labels:  m.Labels,
		},
		Capabilities: capabilities,
		Bindings:     bindings,
		Params:       append([]core.Port(nil), assembly.Params...),
		Result:       normalizedResult(assembly.Result),
	}
}

// normalizedResult renders an Assembly result for the hash. A result that
// declares neither a schema nor any mapping contributes nothing, so a
// registered assembly without a result and one with an empty result hash
// identically — old payloads stay comparable to new ones.
func normalizedResult(result *core.AssemblyResult) *normalizedAssemblyResult {
	if result == nil {
		return nil
	}
	normalized := &normalizedAssemblyResult{
		Schema: append([]core.Port(nil), result.Schema...),
	}
	for _, field := range result.Fields {
		normalized.Fields = append(normalized.Fields, normalizedAssemblyResultField{
			Name:   field.Name,
			Source: field.Source,
		})
	}
	if len(normalized.Schema) == 0 && len(normalized.Fields) == 0 {
		return nil
	}
	return normalized
}
