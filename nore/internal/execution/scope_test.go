package execution

import (
	"context"
	"errors"
	"sync"
	"testing"

	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

// Cancellation is only meaningful if the work actually observes it. A scope that
// cannot be reached by an execution ID would silently cancel nothing while
// reporting success.
func TestBoundScopeIsReachableAndCancels(t *testing.T) {
	scopes := NewScopeRegistry(context.Background())
	scopes.Bind("exec_1")

	ctx, ok := scopes.Context("exec_1")
	if !ok {
		t.Fatal("Context() = false for a bound execution, want true")
	}
	if err := ctx.Err(); err != nil {
		t.Fatalf("bound scope is already %v, want it live", err)
	}

	if !scopes.Cancel("exec_1") {
		t.Fatal("Cancel() = false for a bound execution, want true")
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("scope error = %v, want context.Canceled", ctx.Err())
	}
}

// Cancelling something that is not running must not be reported as success, or
// a caller believes it stopped work that never existed.
func TestCancellingAnUnknownExecutionIsRefused(t *testing.T) {
	scopes := NewScopeRegistry(context.Background())
	if scopes.Cancel("exec_missing") {
		t.Error("Cancel() = true for an unbound execution, want false")
	}
}

// Cancelling is idempotent and must stay that way: the scheduler cancels before
// it records the state, and Release cancels again. Neither race may turn into an
// error, and neither may cancel anything other than this execution's scope.
//
// Deciding whether the *caller* got to stop the execution is deliberately not
// this function's job -- that is MarkCancelled's answer, and it is the only one
// the API reports to a client.
func TestCancellingTwiceIsIdempotent(t *testing.T) {
	scopes := NewScopeRegistry(context.Background())
	scopes.Bind("exec_1")
	ctx, _ := scopes.Context("exec_1")

	scopes.Cancel("exec_1")
	scopes.Cancel("exec_1")

	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("scope error = %v, want context.Canceled", ctx.Err())
	}
}

// A released scope is finished business. Cancelling it would be claiming to
// affect work that has already stopped reporting for.
func TestReleasedScopeIsNotCancellable(t *testing.T) {
	scopes := NewScopeRegistry(context.Background())
	scopes.Bind("exec_1")
	scopes.Release("exec_1")

	if _, ok := scopes.Context("exec_1"); ok {
		t.Error("Context() = true after Release(), want false")
	}
	if scopes.Cancel("exec_1") {
		t.Error("Cancel() = true after Release(), want false")
	}
}

// Instance shutdown cancels the scheduler's context, and every execution scope
// derives from it. That inheritance is the only reason stopping an instance
// actually stops the capabilities it was running instead of orphaning work that
// keeps executing against a torn-down runtime.
func TestBindInheritsParentCancellationSoInstanceShutdownReachesWork(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	scopes := NewScopeRegistry(parent)
	scopes.Bind("exec_1")
	ctx, ok := scopes.Context("exec_1")
	if !ok {
		t.Fatal("Context() = false, want true")
	}

	cancelParent()

	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("scope error after the parent died = %v, want context.Canceled", ctx.Err())
	}
}

// Binding after the parent is already dead yields an already-stopped scope. That
// is the honest answer rather than a bug: the instance is shutting down, so the
// execution can never finish, and starting it would only produce a spurious
// failure instead of a prompt refusal.
func TestBindAfterParentDeathYieldsAStoppedScope(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	scopes := NewScopeRegistry(parent)
	cancelParent()
	<-parent.Done()

	ctx := scopes.Bind("exec_1")

	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("scope error = %v, want context.Canceled for work bound after shutdown began", ctx.Err())
	}
}

// Releasing must leave sibling executions running. Instance shutdown releases
// scopes one at a time as executions finish, and a released one taking its
// neighbours with it would abandon healthy work.
func TestReleaseLeavesOtherScopesRunning(t *testing.T) {
	scopes := NewScopeRegistry(context.Background())
	scopes.Bind("exec_1")
	scopes.Bind("exec_2")

	scopes.Release("exec_1")

	if _, ok := scopes.Context("exec_2"); !ok {
		t.Fatal("Context() = false for the sibling scope, want true")
	}
	sibling, _ := scopes.Context("exec_2")
	if err := sibling.Err(); err != nil {
		t.Fatalf("sibling scope is %v, want it still live", err)
	}
}

// ReleaseAll is the end of the process's interest: bindings go away so a long
// lived daemon does not accumulate a scope per execution it has finished.
func TestReleaseAllForgetsEveryBinding(t *testing.T) {
	scopes := NewScopeRegistry(context.Background())
	scopes.Bind("exec_1")
	scopes.Bind("exec_2")

	scopes.ReleaseAll()

	if _, ok := scopes.Context("exec_1"); ok {
		t.Error("Context() = true after ReleaseAll(), want false")
	}
	if _, ok := scopes.Context("exec_2"); ok {
		t.Error("Context() = true after ReleaseAll(), want false")
	}
}

// ReleaseAll is what an instance calls the moment it starts shutting down. If it
// cancelled a detached scope, detached work would abort at the instant shutdown
// began and the engine's drain budget could never apply, which defeats the
// purpose of detaching in the first place.
func TestReleaseAllLeavesDetachedWorkAlone(t *testing.T) {
	scopes := NewScopeRegistry(context.Background())
	ordinary := scopes.Bind("exec_1")
	detached := scopes.BindDetached("task_1")

	scopes.ReleaseAll()

	if !errors.Is(ordinary.Err(), context.Canceled) {
		t.Errorf("ordinary scope error = %v, want context.Canceled", ordinary.Err())
	}
	if err := detached.Err(); err != nil {
		t.Errorf("detached scope error = %v, want it still live so the drain budget can bound it", err)
	}
}

// A detached task must still be stoppable. Its bound comes from the drain
// timeout or from Release when it goes terminal, so both paths have to work.
func TestDetachedScopeRemainsCancellable(t *testing.T) {
	t.Run("Cancel", func(t *testing.T) {
		scopes := NewScopeRegistry(context.Background())
		detached := scopes.BindDetached("task_1")

		if !scopes.Cancel("task_1") {
			t.Fatal("Cancel() = false for a bound detached execution, want true")
		}
		if !errors.Is(detached.Err(), context.Canceled) {
			t.Errorf("detached scope error = %v, want context.Canceled", detached.Err())
		}
	})

	t.Run("Release on terminal", func(t *testing.T) {
		scopes := NewScopeRegistry(context.Background())
		detached := scopes.BindDetached("task_1")

		scopes.Release("task_1")

		if !errors.Is(detached.Err(), context.Canceled) {
			t.Errorf("detached scope error = %v, want context.Canceled", detached.Err())
		}
		if _, ok := scopes.Context("task_1"); ok {
			t.Error("Context() = true after Release(), want false")
		}
	})

}

// BindDetached must not hand out a second context for an execution that was
// already bound, exactly like Bind. A redelivered ExecutionStarted event would
// otherwise give detached work a scope that no drain budget reaches.
func TestBindDetachedIsIdempotent(t *testing.T) {
	scopes := NewScopeRegistry(context.Background())
	first := scopes.BindDetached("task_1")
	second := scopes.BindDetached("task_1")

	if first != second {
		t.Fatal("BindDetached() returned a different context on rebind, want the same scope")
	}
}

// An execution's detachment does not change once it is bound, so a rebind that
// arrives after the first Bind must keep the original scope's disposition.
func TestRebindKeepsTheFirstDisposition(t *testing.T) {
	t.Run("detached stays detached", func(t *testing.T) {
		scopes := NewScopeRegistry(context.Background())
		detached := scopes.BindDetached("task_1")
		_ = scopes.Bind("task_1")

		scopes.ReleaseAll()

		if err := detached.Err(); err != nil {
			t.Errorf("detached scope error = %v, want it to survive ReleaseAll", err)
		}
	})

	t.Run("ordinary stays ordinary", func(t *testing.T) {
		scopes := NewScopeRegistry(context.Background())
		ordinary := scopes.Bind("exec_1")
		_ = scopes.BindDetached("exec_1")

		scopes.ReleaseAll()

		if !errors.Is(ordinary.Err(), context.Canceled) {
			t.Errorf("ordinary scope error = %v, want context.Canceled", ordinary.Err())
		}
	})
}

// A detached scope survives ReleaseAll only until the work it bounds settles, so
// Release must still be able to discard it afterwards.
func TestDetachedScopeIsReleasedAfterDraining(t *testing.T) {
	scopes := NewScopeRegistry(context.Background())
	scopes.BindDetached("task_1")

	scopes.ReleaseAll()

	// Still tracked, because the work may still be draining.
	if _, ok := scopes.Context("task_1"); !ok {
		t.Fatal("detached scope was forgotten during ReleaseAll; the drain has nothing left to bound it")
	}

	// The drain finished, so the task went terminal and released its scope.
	scopes.Release("task_1")

	if _, ok := scopes.Context("task_1"); ok {
		t.Error("Context() = true after Release(), want false")
	}
}

// Bind is idempotent by design. The scheduler observes ExecutionStarted, so a
// redelivered event would otherwise hand a capability a second scope that no
// cancellation can reach -- work that keeps running after the caller asked for
// it to stop, with nothing able to prove it is still alive.
func TestBindIsIdempotentSoDuplicateEventsCannotEscapeCancellation(t *testing.T) {
	scopes := NewScopeRegistry(context.Background())
	first := scopes.Bind("exec_1")
	second := scopes.Bind("exec_1")

	if first != second {
		t.Fatal("Bind() returned a different context on rebind, want the same scope")
	}

	scopes.Cancel("exec_1")

	if !errors.Is(first.Err(), context.Canceled) {
		t.Errorf("first reference error = %v, want context.Canceled", first.Err())
	}
	if !errors.Is(second.Err(), context.Canceled) {
		t.Errorf("second reference error = %v, want context.Canceled", second.Err())
	}
}

// An execution without an identity cannot be addressed to cancel, so Bind must
// not fabricate a scope that would look live and leak for the instance's lifetime.
func TestBindWithoutAnIdentityHandsBackTheParent(t *testing.T) {
	scopes := NewScopeRegistry(context.Background())

	ctx := scopes.Bind("")

	if ctx == nil {
		t.Fatal("Bind(\"\") = nil, want the parent context")
	}
	if _, ok := scopes.Context(""); ok {
		t.Error("Context(\"\") = true, want no registered scope for an empty ID")
	}
}

// The registry is written from the scheduler while the engine reads it
// concurrently for every invocation, so this asserts the access pattern holds up
// under the race detector.
func TestConcurrentBindLookupCancelAndRelease(t *testing.T) {
	scopes := NewScopeRegistry(context.Background())
	ids := []shared.ID{"exec_1", "exec_2", "exec_3", "exec_4"}

	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(3)
		go func(id shared.ID) {
			defer wg.Done()
			scopes.Bind(id)
			scopes.Cancel(id)
		}(id)
		go func(id shared.ID) {
			defer wg.Done()
			for range 50 {
				if ctx, ok := scopes.Context(id); ok && ctx.Err() == nil {
					// A live scope is fine; this only exercises the read path.
					_ = ctx
				}
			}
		}(id)
		go func(id shared.ID) {
			defer wg.Done()
			scopes.Release(id)
		}(id)
	}
	wg.Wait()

	// Every ID must still be cancellable-or-forgotten; none may panic or hang.
	scopes.ReleaseAll()
}
