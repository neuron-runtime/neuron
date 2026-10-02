package planner

import (
	"fmt"
	"sort"

	"github.com/neuron-runtime/neuron/nore/internal/types"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

// buildDetachedScopes compiles one execution scope per detached capability.
//
// Detach is a lifecycle boundary, not a severed dependency. A detached
// capability keeps its bindings, contracts, and failure semantics; what changes
// is that the work it and everything downstream of it represents is handed to a
// separately tracked execution that may outlive its caller. Compiling that scope
// here means splitting an execution at runtime costs no graph work, and the
// registered assembly is never modified.
//
// Scope ownership is unambiguous because Compile rejects a capability with more
// than one incoming binding. Every capability therefore has exactly one parent,
// the capabilities reachable from a detached root form a tree, and a detached
// capability nested inside another detached scope has exactly one enclosing
// boundary. That property is asserted here rather than assumed, so a later
// relaxation of fan-in cannot silently give a scope two owners.
func buildDetachedScopes(nodes map[shared.ID]types.ExecutionNode, metadata shared.Metadata) (map[shared.ID]*types.ExecutionBlueprint, error) {
	roots := detachedRoots(nodes)
	if len(roots) == 0 {
		return nil, nil
	}

	reachable := make(map[shared.ID]map[shared.ID]struct{}, len(roots))
	for _, root := range roots {
		reachable[root] = reachableFrom(nodes, root)
	}
	if err := assertSingleOwnerPerScope(roots, reachable); err != nil {
		return nil, err
	}

	scopes := make(map[shared.ID]*types.ExecutionBlueprint, len(roots))
	visiting := make(map[shared.ID]bool, len(roots))

	var build func(root shared.ID) (*types.ExecutionBlueprint, error)
	build = func(root shared.ID) (*types.ExecutionBlueprint, error) {
		if scope, exists := scopes[root]; exists {
			return scope, nil
		}
		if visiting[root] {
			// Compile rejects binding cycles before reaching this point. The
			// guard keeps a future relaxation of that rule from turning into
			// unbounded recursion here.
			return nil, fmt.Errorf("detached capability %s is part of a binding cycle", root)
		}
		visiting[root] = true
		defer delete(visiting, root)

		scopeNodes := make(map[shared.ID]types.ExecutionNode, len(reachable[root]))
		for id := range reachable[root] {
			scopeNodes[id] = nodes[id]
		}
		// The detached capability runs inside its own scope. Detach describes
		// the enclosing execution's relationship to it, not its own behavior, so
		// leaving the mode set would make the task detach the same capability
		// again and never run it.
		entry := scopeNodes[root]
		entry.Capability = cloneCapability(entry.Capability)
		entry.Capability.RuntimeConfig.Execution.Mode = shared.RuntimeExecutionModeWait
		scopeNodes[root] = entry

		// A detached capability below this root becomes its own scope inside
		// this one. The root itself is this scope's entry, not a nested scope.
		var nested map[shared.ID]*types.ExecutionBlueprint
		for _, id := range sortedIDs(reachable[root]) {
			if id == root || !isDetached(nodes[id]) {
				continue
			}
			child, err := build(id)
			if err != nil {
				return nil, err
			}
			if nested == nil {
				nested = make(map[shared.ID]*types.ExecutionBlueprint)
			}
			nested[id] = child
		}

		scope := &types.ExecutionBlueprint{
			Metadata:           cloneMetadata(metadata),
			Nodes:              scopeNodes,
			EntryCapabilityIDs: []shared.ID{root},
			Detached:           nested,
		}
		scopes[root] = scope
		return scope, nil
	}

	for _, root := range roots {
		if _, err := build(root); err != nil {
			return nil, err
		}
	}
	return scopes, nil
}

// detachedRoots returns every detached capability in the plan, in a stable order
// so that both compilation and its error messages are deterministic.
func detachedRoots(nodes map[shared.ID]types.ExecutionNode) []shared.ID {
	roots := make([]shared.ID, 0, len(nodes))
	for _, id := range sortedIDs(nodes) {
		if isDetached(nodes[id]) {
			roots = append(roots, id)
		}
	}
	return roots
}

// assertSingleOwnerPerScope rejects a plan where one detached capability is
// reachable from more than one enclosing detached boundary. Such a scope would
// have two candidate owners and no defensible way to choose between them.
//
// Reachable sets may legitimately overlap — a nested detached capability is
// reachable from its enclosing root — so this counts enclosing roots per
// detached capability rather than the reverse.
func assertSingleOwnerPerScope(roots []shared.ID, reachable map[shared.ID]map[shared.ID]struct{}) error {
	for _, scope := range roots {
		var owners []shared.ID
		for _, candidate := range roots {
			if candidate == scope {
				continue
			}
			if _, enclosed := reachable[candidate][scope]; enclosed {
				owners = append(owners, candidate)
			}
		}
		if len(owners) > 1 {
			return fmt.Errorf(
				"detached capability %s is reachable from multiple detached capabilities (%v); "+
					"use an aggregation capability so the scope has a single owner",
				scope, owners)
		}
	}
	return nil
}

// reachableFrom collects every capability reachable from root, including root
// itself. An explicit worklist keeps traversal iterative so a long binding chain
// cannot exhaust the stack.
func reachableFrom(nodes map[shared.ID]types.ExecutionNode, root shared.ID) map[shared.ID]struct{} {
	seen := map[shared.ID]struct{}{root: {}}
	queue := []shared.ID{root}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, transition := range nodes[current].Next {
			target := transition.TargetCapabilityID
			if _, visited := seen[target]; visited {
				continue
			}
			seen[target] = struct{}{}
			queue = append(queue, target)
		}
	}
	return seen
}

// isDetached reports whether a capability declared that N.O.R.E. should hand its
// work off instead of waiting for it. The effective runtimeConfig has already
// been resolved onto the plan, so an omitted mode is already the default.
func isDetached(node types.ExecutionNode) bool {
	execution := node.Capability.RuntimeConfig.Execution
	return execution != nil && execution.Mode == shared.RuntimeExecutionModeDetach
}

func sortedIDs[T any](values map[shared.ID]T) []shared.ID {
	ids := make([]shared.ID, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
