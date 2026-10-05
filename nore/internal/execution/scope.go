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
	ctx      context.Context
	cancel   context.CancelFunc
	detached bool
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
	return r.bind(executionID, false)
}

// BindDetached binds the scope of a task whose purpose is to outlive the
// execution that created it, including outliving the instance shutting down.
//
// A detached scope is the one scope that ReleaseAll must not cancel, because the
// drain budget in the engine's invocation context is what bounds the work
// instead. Cancelling it here would end detached work at the instant shutdown
// began, which is precisely what detach exists to prevent.
func (r *ScopeRegistry) BindDetached(executionID shared.ID) context.Context {
	return r.bind(executionID, true)
}

func (r *ScopeRegistry) bind(executionID shared.ID, detached bool) context.Context {
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
	// A detached scope must not inherit the instance's cancellation: the whole
	// point of detaching is that the work outlives the instance, so deriving from
	// the instance context would end it the moment shutdown began even though the
	// scope itself was never cancelled. Its bound comes from the engine's drain
	// budget, or from Cancel or Release when the task stops for another reason.
	parent := r.parent
	if detached {
		parent = context.WithoutCancel(parent)
	}
	ctx, cancel := context.WithCancel(parent)
	r.scopes[executionID] = &executionScope{ctx: ctx, cancel: cancel, detached: detached}
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
// Release applies to a detached scope as well: a detached task that reached a
// terminal state has finished its work, so the drain budget no longer applies to
// it.
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

// ReleaseAll discards and cancels every bound scope except those bound for
// detached work.
//
// An ordinary execution holds no live work once the scheduler has stopped, so its
// scope can be dropped at once. A detached scope is the exception: the work it
// bounds is meant to outlive the instance, and the engine is already draining it
// under its own timeout. Cancelling here would end that work immediately and make
// the drain budget unreachable.
//
// A detached scope is still bounded, by exactly one of two things: the drain
// timeout expiring, or Release being called when the task reaches a terminal
// state. Neither requires this registry, which is what lets it stop tracking
// detached scopes at shutdown.
func (r *ScopeRegistry) ReleaseAll() {
	r.mu.Lock()
	ordinary := make([]*executionScope, 0, len(r.scopes))
	for executionID, scope := range r.scopes {
		if scope.detached {
			continue
		}
		ordinary = append(ordinary, scope)
		delete(r.scopes, executionID)
	}
	r.mu.Unlock()
	for _, scope := range ordinary {
		scope.cancel()
	}
}
