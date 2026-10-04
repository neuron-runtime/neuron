package execution

import (
	"context"
	"sync"

	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

// ScopeRegistry owns the cancellable context of every live execution.
//
// The scope lives here rather than on Execution because an Execution is a
// persisted value object: it is marshalled to a snapshot, written to storage,
// and rebuilt from that snapshot after a restart. A context is a runtime
// lifetime, so holding one on the model would put a live handle inside a value
// that outlives the process. This is the same reason Execution.done exists and
// is likewise excluded from the snapshot.
//
// The registry is an internal collaborator rather than a contracts interface:
// scheduler and engine already share the ExecutionRepository, and introducing a
// second seam between them would abstract nothing that is not already shared.
type ScopeRegistry struct {
	parent context.Context
	mu     sync.Mutex
	scopes map[shared.ID]*executionScope
}

// executionScope is one execution's cancellable context. cancel is guarded by
// sync.Once through the registry, so calling Cancel twice is safe and cancels
// exactly once.
type executionScope struct {
	ctx    context.Context
	cancel context.CancelFunc
}

// NewScopeRegistry returns a registry whose scopes derive from parent, so
// cancelling the instance cancels every execution running under it.
func NewScopeRegistry(parent context.Context) *ScopeRegistry {
	if parent == nil {
		parent = context.Background()
	}
	return &ScopeRegistry{parent: parent, scopes: make(map[shared.ID]*executionScope)}
}

// Bind returns the cancellable context for an execution, creating the scope on
// first use. It is idempotent: a second Bind returns the context that is
// already in force, so a scheduler that observes the same ExecutionStarted event
// twice cannot hand a capability a context that escapes its own cancellation.
func (r *ScopeRegistry) Bind(executionID shared.ID) context.Context {
	if executionID == "" {
		// An execution without an identity cannot be addressed to cancel. Handing
		// back the parent keeps the caller running rather than panicking on a
		// value that cannot exist in the repository.
		return r.parent
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if scope, exists := r.scopes[executionID]; exists {
		return scope.ctx
	}
	ctx, cancel := context.WithCancel(r.parent)
	r.scopes[executionID] = &executionScope{ctx: ctx, cancel: cancel}
	return ctx
}

// Context returns the scope bound to an execution, reporting false when the
// execution has no live scope: it never started, or it has already been
// released. A caller that needs a context regardless uses Bind.
func (r *ScopeRegistry) Context(executionID shared.ID) (context.Context, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	scope, exists := r.scopes[executionID]
	if !exists {
		return nil, false
	}
	return scope.ctx, true
}

// Cancel ends an execution's scope, which aborts every capability invocation
// running under it. It reports whether a scope was bound; cancelling an unknown
// or already-released execution is not an error, because the caller's intent
// that the execution stop is satisfied either way.
//
// Cancelling does not by itself make the execution terminal. The caller records
// that with MarkCancelled; the scope only stops the work.
func (r *ScopeRegistry) Cancel(executionID shared.ID) bool {
	r.mu.Lock()
	scope, exists := r.scopes[executionID]
	r.mu.Unlock()
	if !exists {
		return false
	}
	scope.cancel()
	return true
}

// Release discards an execution's scope and cancels it, freeing the context for
// garbage collection. It runs when an execution reaches a terminal state: a
// finished execution holds no live work, so leaving its context registered would
// retain every value derived from it for the lifetime of the instance.
//
// Release is idempotent and safe to call for an execution that was never bound.
func (r *ScopeRegistry) Release(executionID shared.ID) {
	r.mu.Lock()
	scope, exists := r.scopes[executionID]
	delete(r.scopes, executionID)
	r.mu.Unlock()
	if exists {
		scope.cancel()
	}
}

// ReleaseAll discards every scope. An instance calls it once its scheduler and
// engine have stopped, so no capability can be mid-invocation and every
// remaining context can be dropped at once.
func (r *ScopeRegistry) ReleaseAll() {
	r.mu.Lock()
	scopes := r.scopes
	r.scopes = make(map[shared.ID]*executionScope)
	r.mu.Unlock()
	for _, scope := range scopes {
		scope.cancel()
	}
}

// CancelAll ends every bound scope without releasing it, so each execution can
// still be recorded as cancelled by whoever owns it.
func (r *ScopeRegistry) CancelAll() {
	r.mu.Lock()
	scopes := make([]*executionScope, 0, len(r.scopes))
	for _, scope := range r.scopes {
		scopes = append(scopes, scope)
	}
	r.mu.Unlock()
	for _, scope := range scopes {
		scope.cancel()
	}
}
