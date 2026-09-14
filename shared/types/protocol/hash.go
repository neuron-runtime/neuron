package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"github.com/Muhammad-Jay/neuron/shared/types/core"
)

// HashSystem returns a deterministic content hash for a System.
//
// The parser generates random IDs (system and connector metadata IDs) on every
// parse, so the raw struct cannot be hashed directly. HashSystem normalizes
// the system first: generated IDs are dropped, services and connectors are
// sorted, and map marshaling (key-sorted by encoding/json) stays stable.
func HashSystem(system core.System) (string, error) {
	data, err := json.Marshal(normalizeSystem(system))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// SystemKey derives the identity of a System from its metadata. Hash is
// computed deterministically via HashSystem.
func SystemKey(system core.System, env string) (InstanceKey, error) {
	hash, err := HashSystem(system)
	if err != nil {
		return InstanceKey{}, err
	}
	id := system.Metadata.Name
	if id == "" {
		id = "system"
	}
	version := system.Metadata.Version
	if version == "" {
		version = "latest"
	}
	if env == "" {
		env = "development"
	}
	return InstanceKey{SystemID: id, Version: version, Hash: hash, Env: env}, nil
}

type normalizedSystem struct {
	Metadata   normalizedMetadata    `json:"metadata"`
	Services   []normalizedService   `json:"services"`
	Connectors []normalizedConnector `json:"connectors"`
}

type normalizedMetadata struct {
	Name        string            `json:"name"`
	Version     string            `json:"version,omitempty"`
	Description string            `json:"description,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

type normalizedService struct {
	ID          string            `json:"id"`
	Type        core.ExecutorType `json:"type"`
	Config      map[string]any    `json:"config"`
	Inputs      []core.Port       `json:"inputs,omitempty"`
	Outputs     []core.Port       `json:"outputs,omitempty"`
	Timeout     string            `json:"timeout,omitempty"`
	MaxAttempts int               `json:"max_attempts,omitempty"`
	Backoff     string            `json:"backoff,omitempty"`
}

type normalizedConnector struct {
	From        string                `json:"from"`
	To          string                `json:"to"`
	Mappings    []core.MappingRule    `json:"mappings,omitempty"`
	Validations []core.ValidationRule `json:"validations,omitempty"`
}

func normalizeSystem(system core.System) normalizedSystem {
	m := system.Metadata
	services := make([]normalizedService, 0, len(system.Specification.Services)+len(system.Specification.Triggers))
	for _, trigger := range system.Specification.Triggers {
		services = append(services, normalizedService{
			ID:          string(trigger.Metadata.ID),
			Type:        trigger.Type,
			Config:      trigger.ServiceConfigurations,
			Inputs:      append([]core.Port(nil), trigger.Inputs...),
			Outputs:     append([]core.Port(nil), trigger.Outputs...),
			Timeout:     trigger.RuntimeConfigurations.Timeout,
			MaxAttempts: trigger.RuntimeConfigurations.Retry.MaxAttempts,
			Backoff:     trigger.RuntimeConfigurations.Retry.Backoff,
		})
	}
	for _, svc := range system.Specification.Services {
		services = append(services, normalizedService{
			ID:          string(svc.Metadata.ID),
			Type:        svc.Type,
			Config:      svc.ServiceConfigurations,
			Inputs:      append([]core.Port(nil), svc.Inputs...),
			Outputs:     append([]core.Port(nil), svc.Outputs...),
			Timeout:     svc.RuntimeConfigurations.Timeout,
			MaxAttempts: svc.RuntimeConfigurations.Retry.MaxAttempts,
			Backoff:     svc.RuntimeConfigurations.Retry.Backoff,
		})
	}
	sort.Slice(services, func(i, j int) bool { return services[i].ID < services[j].ID })

	connectors := make([]normalizedConnector, 0, len(system.Specification.Connectors))
	for _, conn := range system.Specification.Connectors {
		mappings := append([]core.MappingRule(nil), conn.Mappings...)
		sort.Slice(mappings, func(i, j int) bool { return mappings[i].TargetPath < mappings[j].TargetPath })
		validations := append([]core.ValidationRule(nil), conn.Validations...)
		sort.Slice(validations, func(i, j int) bool { return validations[i].Expression < validations[j].Expression })
		connectors = append(connectors, normalizedConnector{
			From:        string(conn.From.ServiceID),
			To:          string(conn.To.ServiceID),
			Mappings:    mappings,
			Validations: validations,
		})
	}
	sort.Slice(connectors, func(i, j int) bool {
		if connectors[i].From != connectors[j].From {
			return connectors[i].From < connectors[j].From
		}
		return connectors[i].To < connectors[j].To
	})

	return normalizedSystem{
		Metadata: normalizedMetadata{
			Name:        m.Name,
			Version:     m.Version,
			Description: m.Description,
			Labels:      m.Labels,
		},
		Services:   services,
		Connectors: connectors,
	}
}
