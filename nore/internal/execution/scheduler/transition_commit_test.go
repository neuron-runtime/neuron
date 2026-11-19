package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/event"
	executionmodel "github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/nore/internal/execution/engine"
	core "github.com/neuron-runtime/neuron/shared/types/core"
)

// fakeCommitter is a TransitionCommitter that records the transitions it was
// asked to commit. It can hold the first commit open (entered/release) so a test
// can observe what has and has not been announced while the commit is in flight,
// and it can inject a commit failure for a chosen snapshot.
type fakeCommitter struct {
	mu       sync.Mutex
	commits  []fakeCommit
	failWhen func(*executionmodel.Execution) error

	entered chan struct{}
	release chan struct{}
}

type fakeCommit struct {
	status  executionmodel.Status
	intents []*executionmodel.DispatchIntent
}

func (c *fakeCommitter) SaveTransition(_ context.Context, execution *executionmodel.Execution, intents []*executionmodel.DispatchIntent) error {
	if c.entered != nil {
		select {
		case c.entered <- struct{}{}:
		default:
		}
		<-c.release
	}
	if c.failWhen != nil {
		if err := c.failWhen(execution); err != nil {
			return err
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.commits = append(c.commits, fakeCommit{status: execution.Status(), intents: intents})
	return nil
}

func (c *fakeCommitter) ClaimDispatchIntent(_ context.Context, intent *executionmodel.DispatchIntent, _ string, _ time.Duration) (*executionmodel.DispatchIntent, bool, error) {
	return intent, true, nil
}

func (c *fakeCommitter) ReleaseDispatchIntent(context.Context, core.ID, core.ID, core.ID, string) error {
	return nil
}

func (c *fakeCommitter) CompleteDispatchIntent(context.Context, core.ID, core.ID, core.ID, string) error {
	return nil
}

func (c *fakeCommitter) snapshot() []fakeCommit {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]fakeCommit(nil), c.commits...)
}

// TestSchedulerCommitsTransitionBeforeAnnouncingReady draws the durable-mode
// ownership boundary: a transition and the dispatch intents it produced must be
// committed before the event that announces it is published. The commit is held
// open while the test checks that nothing has been announced; a scheduler that
// published first would be observed here.
func TestSchedulerCommitsTransitionBeforeAnnouncingReady(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus := event.NewBus()
	store := executionmodel.NewMemoryStore()
	scopes := executionmodel.NewScopeRegistry(ctx)

	committer := &fakeCommitter{entered: make(chan struct{}, 1), release: make(chan struct{})}
	sched, err := New(bus, store, scopes, WithTransitionCommitter(committer))
	if err != nil {
		t.Fatalf("New scheduler: %v", err)
	}

	ready, err := bus.Subscribe(event.CapabilityReady, 4)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer func() { _ = ready.Close() }()

	go func() { _ = sched.Run(ctx) }()

	exec := startExecution(t, ctx, bus, store, singleCapabilityBlueprint("cap"))

	select {
	case <-committer.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the scheduler never committed the entry transition")
	}

	select {
	case evt := <-ready.Events():
		t.Fatalf("CapabilityReady was announced before the transition committed: %v", evt.Type)
	case <-time.After(50 * time.Millisecond):
	}

	close(committer.release)

	select {
	case evt := <-ready.Events():
		if evt.Type != event.CapabilityReady {
			t.Fatalf("first event after the commit = %v, want %v", evt.Type, event.CapabilityReady)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CapabilityReady was never announced after the commit")
	}

	commits := committer.snapshot()
	if len(commits) != 1 {
		t.Fatalf("entry transition committed %d times, want 1", len(commits))
	}
	if commits[0].status != executionmodel.StatusRunning {
		t.Fatalf("committed status = %s, want %s", commits[0].status, executionmodel.StatusRunning)
	}
	if len(commits[0].intents) != 1 {
		t.Fatalf("entry transition committed %d intents, want 1", len(commits[0].intents))
	}
	intent := commits[0].intents[0]
	if intent.ExecutionID != exec.ID || intent.CapabilityID != "cap" || !intent.ReadyToRun {
		t.Fatalf("committed intent = %+v, want a ready intent for %s/cap", intent, exec.ID)
	}
}

// TestSchedulerFailsExecutionWhenCompletionCannotBeCommitted covers the failure
// half of the boundary: when the terminal snapshot cannot be committed, the
// execution must not be reported as completed. The scheduler flips it to failed
// and announces the failure instead.
func TestSchedulerFailsExecutionWhenCompletionCannotBeCommitted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus := event.NewBus()
	store := executionmodel.NewMemoryStore()
	scopes := executionmodel.NewScopeRegistry(ctx)

	errCommit := errors.New("commit unavailable")
	committer := &fakeCommitter{failWhen: func(execution *executionmodel.Execution) error {
		if execution.Status() == executionmodel.StatusCompleted {
			return errCommit
		}
		return nil
	}}

	sched, err := New(bus, store, scopes, WithTransitionCommitter(committer))
	if err != nil {
		t.Fatalf("New scheduler: %v", err)
	}
	eng, err := engine.NewCapabilityRuntimeEngine(bus, stubRegistry{runtime: &recordingRuntime{}}, store, scopes, 2, time.Second, engine.WithTransitionCommitter(committer))
	if err != nil {
		t.Fatalf("New engine: %v", err)
	}

	go func() { _ = sched.Run(ctx) }()
	go func() { _ = eng.Run(ctx) }()

	exec := startExecution(t, ctx, bus, store, singleCapabilityBlueprint("cap"))

	waitFor(t, "the execution to reach a terminal state", exec.IsTerminal)
	if exec.Status() != executionmodel.StatusFailed {
		t.Fatalf("status = %s, want %s: a completion that could not be committed must not be reported as success", exec.Status(), executionmodel.StatusFailed)
	}
}

// TestSchedulerCommitsADrainedExecutionOnce proves the terminal transition is a
// single commit. Committing the running snapshot after the last capability
// finishes and then committing the completed snapshot would leave a durable
// intermediate state with no work outstanding; a restart could observe it and
// never advance it.
func TestSchedulerCommitsADrainedExecutionOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus := event.NewBus()
	store := executionmodel.NewMemoryStore()
	scopes := executionmodel.NewScopeRegistry(ctx)

	committer := &fakeCommitter{}
	sched, err := New(bus, store, scopes, WithTransitionCommitter(committer))
	if err != nil {
		t.Fatalf("New scheduler: %v", err)
	}
	eng, err := engine.NewCapabilityRuntimeEngine(bus, stubRegistry{runtime: &recordingRuntime{}}, store, scopes, 2, time.Second, engine.WithTransitionCommitter(committer))
	if err != nil {
		t.Fatalf("New engine: %v", err)
	}

	go func() { _ = sched.Run(ctx) }()
	go func() { _ = eng.Run(ctx) }()

	startExecution(t, ctx, bus, store, singleCapabilityBlueprint("cap"))

	waitFor(t, "the execution completion to be committed", func() bool {
		commits := committer.snapshot()
		return len(commits) > 0 && commits[len(commits)-1].status == executionmodel.StatusCompleted
	})

	commits := committer.snapshot()
	if len(commits) != 2 {
		t.Fatalf("committed %d transitions, want 2 (running, completed)", len(commits))
	}
	if commits[0].status != executionmodel.StatusRunning {
		t.Fatalf("first commit status = %s, want %s", commits[0].status, executionmodel.StatusRunning)
	}
}
