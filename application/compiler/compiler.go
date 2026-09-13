package compiler

import (
	"fmt"

	"github.com/Muhammad-Jay/neuron/application/compiler/manifest"
	"github.com/Muhammad-Jay/neuron/shared/types/core"
	"github.com/Muhammad-Jay/neuron/shared/types/protocol"
)

// Compiler transforms a canonical System manifest into the runtime
// core.System representation consumed by N.O.R.E. It is source-language
// agnostic: YAML, TypeScript, JSON, or any future frontend all converge
// on manifest.System before reaching this stage.
type Compiler struct{}

// New returns a Compiler.
func New() *Compiler {
	return &Compiler{}
}

// Compile converts a manifest into core.System.
func (c *Compiler) Compile(m *manifest.System) (*core.System, error) {
	if m == nil {
		return nil, fmt.Errorf("manifest is nil")
	}

	sysMeta := core.Metadata{
		ID:          core.NewID("system_"),
		Name:        m.Metadata.Name,
		Description: m.Metadata.Description,
		Version:     m.Metadata.Version,
	}

	services := make([]core.Service, 0, len(m.Services))
	serviceMap := make(map[string]core.Service)

	for _, rs := range m.Services {
		svc := convertService(rs)
		serviceMap[rs.Name] = svc
		services = append(services, svc)
	}

	connectors := make([]core.Connector, 0, len(m.Connectors))
	for _, rc := range m.Connectors {
		conn, err := convertConnector(rc, serviceMap)
		if err != nil {
			return nil, err
		}
		connectors = append(connectors, conn)
	}

	return &core.System{
		Metadata: sysMeta,
		Specification: core.SystemSpec{
			Services:   services,
			Triggers:   nil,
			Connectors: connectors,
		},
	}, nil
}

// InstanceKey computes the protocol.InstanceKey for a manifest.
// The key identity is (systemID, version, hash, env). The hash is
// derived from the compiled core.System. env is the execution
// environment (e.g. "development", "detach") supplied by the caller
// from the effective configuration.
func (c *Compiler) InstanceKey(m *manifest.System, env string) (protocol.InstanceKey, error) {
	sys, err := c.Compile(m)
	if err != nil || sys == nil {
		return protocol.InstanceKey{}, fmt.Errorf("compile manifest for instance key: %w", err)
	}

	hash, err := protocol.HashSystem(*sys)
	if err != nil {
		return protocol.InstanceKey{}, fmt.Errorf("hash system: %w", err)
	}

	if env == "" {
		env = "development"
	}

	return protocol.InstanceKey{
		SystemID: m.Metadata.Name,
		Version:  m.Metadata.Version,
		Hash:     hash,
		Env:      env,
	}, nil
}

func convertService(s manifest.Service) core.Service {
	var inputs, outputs []core.Port
	for _, p := range s.Inputs {
		inputs = append(inputs, core.Port{
			Name:     p.Name,
			Type:     core.ValueType(p.Type),
			Required: p.Required,
		})
	}
	for _, p := range s.Outputs {
		outputs = append(outputs, core.Port{
			Name:     p.Name,
			Type:     core.ValueType(p.Type),
			Required: p.Required,
		})
	}

	rtConfig := core.RuntimeConfigurations{}
	if s.Execution != nil {
		rtConfig.Timeout = s.Execution.Timeout
		rtConfig.Retry = core.RetryPolicy{
			MaxAttempts: s.Execution.Retries,
			Backoff:     "exponential",
		}
	}

	return core.Service{
		Metadata: core.Metadata{
			ID:          core.ID(s.Name),
			Name:        s.Name,
			Description: s.Description,
			Version:     s.Version,
		},
		Type:                  core.ExecutorType(s.Executor.Name),
		ServiceConfigurations: s.Config,
		RuntimeConfigurations: rtConfig,
		Inputs:                inputs,
		Outputs:               outputs,
	}
}

func convertConnector(conn manifest.Connector, serviceMap map[string]core.Service) (core.Connector, error) {
	fromSvc, ok := serviceMap[conn.From]
	if !ok {
		return core.Connector{}, fmt.Errorf("connector from %q to %q: from service %q not found", conn.From, conn.To, conn.From)
	}
	toSvc, ok := serviceMap[conn.To]
	if !ok {
		return core.Connector{}, fmt.Errorf("connector from %q to %q: to service %q not found", conn.From, conn.To, conn.To)
	}

	var mappings []core.MappingRule
	for _, m := range conn.Mappings {
		mappings = append(mappings, core.MappingRule{
			TargetPath: m.Target,
			Expression: m.Expression,
		})
	}

	var validations []core.ValidationRule
	for _, v := range conn.Validations {
		validations = append(validations, core.ValidationRule{
			Expression: v.Expression,
			Message:    v.Message,
		})
	}

	return core.Connector{
		Metadata: core.Metadata{
			ID:   core.NewID("connector_"),
			Name: conn.From + "->" + conn.To,
		},
		From: core.Endpoint{
			ServiceID: fromSvc.Metadata.ID,
		},
		To: core.Endpoint{
			ServiceID: toSvc.Metadata.ID,
		},
		Mappings:    mappings,
		Validations: validations,
	}, nil
}
