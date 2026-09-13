package manifest

import (
	"github.com/Muhammad-Jay/neuron/application/project"
)

// FromResolvedProject converts a resolved YAML system into the canonical
// System manifest. variables come from the project configuration, not from
// the YAML project file itself. This is the bridge between the YAML frontend
// (project/) and the source-language-neutral manifest boundary.
func FromResolvedProject(rp *project.ResolvedProject, variables map[string]any) *System {
	if rp == nil {
		return nil
	}

	s := &System{
		APIVersion: "neuron/v1",
		Kind:       "System",
		Metadata: Metadata{
			Name:        rp.System.Definition.Metadata.Name,
			Version:     rp.System.Definition.Metadata.Version,
			Description: rp.System.Definition.Metadata.Description,
		},
		Inputs:    nil,
		Variables: variables,
	}

	for _, rs := range rp.System.Services {
		s.Services = append(s.Services, serviceFrom(rs))
	}

	for _, rc := range rp.System.Connectors {
		s.Connectors = append(s.Connectors, connectorFrom(rc))
	}

	// Build a flat sequence definition from the connector chain.
	// A topological order is naturally preserved by the YAML resolver,
	// but we derive it from connectors so the definition matches the
	// actual data flow.
	s.Definition = buildDefinition(rp.System.Services, rp.System.Connectors)

	return s
}

func serviceFrom(rs project.ResolvedService) Service {
	spec := rs.Definition.Spec

	svc := Service{
		Name:        rs.Definition.Metadata.Name,
		Version:     rs.Definition.Metadata.Version,
		Description: rs.Definition.Metadata.Description,
		Executor: ExecutorSpec{
			Name:     spec.Executor.Type,
			Version:  spec.Executor.Version,
			Registry: spec.Executor.Source,
		},
		Config: spec.Config,
	}

	for _, m := range spec.Mappings {
		port := Port{
			Name:     m.Target,
			Type:     "any",
			Required: m.Direction == "input",
		}
		if m.Direction == "input" {
			svc.Inputs = append(svc.Inputs, port)
		} else if m.Direction == "output" {
			svc.Outputs = append(svc.Outputs, port)
		}
	}

	if spec.Execution != nil {
		svc.Execution = &ExecutionConfig{
			Mode:           spec.Execution.Mode,
			Timeout:        spec.Execution.Timeout,
			Retries:        spec.Execution.Retries,
			Concurrency:    spec.Execution.Concurrency,
			ContinueOnFail: spec.Execution.ContinueOnFail,
		}
	}

	return svc
}

func connectorFrom(rc project.ResolvedConnector) Connector {
	conn := Connector{
		From: rc.Definition.From,
		To:   rc.Definition.To,
	}

	for _, m := range rc.Definition.Mappings {
		conn.Mappings = append(conn.Mappings, ConnectorMapping{
			Target:     m.Target,
			Expression: m.Expression,
		})
	}

	for _, v := range rc.Definition.Validations {
		conn.Validations = append(conn.Validations, ConnectorValidation{
			Expression: v.Expression,
			Message:    v.Message,
		})
	}

	return conn
}

// buildDefinition derives a flat sequence definition tree from the
// resolved services and connectors. When the connector graph is a simple
// chain, the definition is a single sequence. Parallel branches are not
// expressible in the current YAML model, so this always produces a
// sequence. The definition is preserved for downstream consumers that
// understand the composition AST.
func buildDefinition(services []project.ResolvedService, connectors []project.ResolvedConnector) SystemNode {
	// Identify the entry service(s): those with no incoming connector.
	targets := make(map[string]bool)
	for _, c := range connectors {
		targets[c.Definition.To] = true
	}

	var entries []string
	for _, s := range services {
		if !targets[s.Ref] {
			entries = append(entries, s.Ref)
		}
	}

	// If the graph is a linear chain (which the YAML model produces),
	// we can simply walk from entry to entry. Otherwise fall back to a
	// service list in resolved order.
	order := topologicalServiceOrder(services, connectors)

	if len(order) == 0 {
		return SystemNode{Kind: "sequence"}
	}

	seq := SystemNode{Kind: "sequence"}
	for _, ref := range order {
		seq.Steps = append(seq.Steps, SystemNode{
			Kind:    "service",
			Service: ref,
		})
	}
	return seq
}

// topologicalServiceOrder returns service refs in dependency order such
// that every connector source precedes its target.
func topologicalServiceOrder(services []project.ResolvedService, connectors []project.ResolvedConnector) []string {
	// Build adjacency + in-degree.
	index := make(map[string]bool)
	for _, s := range services {
		index[s.Ref] = true
	}

	// Only consider connectors whose endpoints exist.
	type edge struct{ from, to string }
	var edges []edge
	for _, c := range connectors {
		if index[c.Definition.From] && index[c.Definition.To] {
			edges = append(edges, edge{c.Definition.From, c.Definition.To})
		}
	}

	inDegree := make(map[string]int)
	adj := make(map[string][]string)
	for _, s := range services {
		inDegree[s.Ref] = 0
	}
	for _, e := range edges {
		inDegree[e.to]++
		adj[e.from] = append(adj[e.from], e.to)
	}

	// Kahn's algorithm.
	var queue []string
	for ref, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, ref)
		}
	}

	var order []string
	for len(queue) > 0 {
		ref := queue[0]
		queue = queue[1:]
		order = append(order, ref)
		for _, next := range adj[ref] {
			inDegree[next]--
			if inDegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}

	// If there are cycles, fall back to resolved order.
	if len(order) != len(services) {
		order = order[:0]
		for _, s := range services {
			order = append(order, s.Ref)
		}
	}

	return order
}
