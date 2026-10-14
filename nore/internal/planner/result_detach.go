package planner

import (
	"fmt"

	"github.com/neuron-runtime/neuron/nore/internal/types"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

// validateResultDetachment rejects an Assembly result field whose structured
// source reads from a capability that will not complete inside the root
// execution: a capability whose effective execution mode is `detach`, or any
// capability downstream of one.
//
// Detach is a lifecycle boundary, not a severed dependency. The detached
// capability and everything reachable from it run in a separately tracked
// execution that the parent does not wait for, so the parent never records
// their params or results. A reference into that branch could therefore only
// fail when the result is resolved at runtime; rejecting it here fails the
// build instead, naming the field, the detached capability that owns the
// branch, and the offending reference.
//
// Validation runs where both facts are simultaneously available — the effective
// execution mode and the binding graph — which is why it lives in the planner
// rather than in the authoring or manifest-compile layers. The planner compiles
// every authoring surface and every persisted registration, so a result that
// reaches a plan through any path is checked.
func validateResultDetachment(result *types.ExecutionResult, nodes map[shared.ID]types.ExecutionNode) error {
	if result == nil {
		return nil
	}
	owners := detachedBranchOwners(nodes)
	for _, field := range result.Fields {
		source := field.Source
		if source == nil {
			continue
		}
		if source.Kind != shared.ValueRefCapabilityResult && source.Kind != shared.ValueRefCapabilityParams {
			continue
		}
		owner, detached := owners[shared.ID(source.Capability)]
		if !detached {
			continue
		}
		return fmt.Errorf(
			"assembly result field %q references capability %q, which is inside the detached branch owned by %q; "+
				"a detached branch runs outside the root execution and its values cannot contribute to the assembly result",
			field.Name, source.Capability, owner,
		)
	}
	return nil
}

// detachedBranchOwners maps every capability that runs inside a detached branch
// — a detached capability itself and everything reachable from it — to the
// detached capability that owns that branch. Capabilities outside any detached
// branch are absent.
//
// A nested detached capability is itself a detached root and is also reachable
// from its enclosing root. Reporting the enclosing (outermost) owner is both
// more useful to an author and deterministic: the enclosing root is the
// capability whose detach actually removes the whole branch from the root
// execution, so an enclosed root is skipped in favour of it.
func detachedBranchOwners(nodes map[shared.ID]types.ExecutionNode) map[shared.ID]shared.ID {
	roots := detachedRoots(nodes)
	if len(roots) == 0 {
		return nil
	}

	reachable := make(map[shared.ID]map[shared.ID]struct{}, len(roots))
	for _, root := range roots {
		reachable[root] = reachableFrom(nodes, root)
	}

	owners := make(map[shared.ID]shared.ID)
	for _, root := range roots {
		if isEnclosedRoot(root, roots, reachable) {
			continue
		}
		for id := range reachable[root] {
			owners[id] = root
		}
	}
	return owners
}

// isEnclosedRoot reports whether candidate is reachable from another detached
// root, i.e. it sits inside an enclosing detached branch rather than at its top.
// Compile rejects a capability reachable from more than one detached root, so at
// most one non-enclosed root can own any capability.
func isEnclosedRoot(candidate shared.ID, roots []shared.ID, reachable map[shared.ID]map[shared.ID]struct{}) bool {
	for _, root := range roots {
		if root == candidate {
			continue
		}
		if _, enclosed := reachable[root][candidate]; enclosed {
			return true
		}
	}
	return false
}
