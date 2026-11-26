package reconciler

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	executionmodel "github.com/neuron-runtime/neuron/nore/internal/execution"
	core "github.com/neuron-runtime/neuron/shared/types/core"
)

// operationLog is one ordered log shared by the claimer and bus fakes. Separate
// per-fake logs cannot prove cross-port ordering: concatenating them looks the
// same whether publish happened before or after release.
type operationLog struct {
	mu      sync.Mutex
	entries []string
}

func (l *operationLog) add(operation string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, operation)
}

func (l *operationLog) snapshot() []string {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.entries...)
}

// minimalClaimer implements exactly the consumer-local port. Its compilation
// proves New does not require intent completion to construct a reconciler.
type minimalClaimer struct{}

func (*minimalClaimer) ClaimReadyDispatchIntents(context.Context, core.ID, string, time.Duration, int) ([]*executionmodel.DispatchIntent, error) {
	return nil, nil
}

func (*minimalClaimer) ReleaseDispatchIntent(context.Context, core.ID, core.ID, core.ID, string) error {
	return nil
}

var _ Claimer = (*minimalClaimer)(nil)

func TestClaimerPortExcludesDispatchCompletion(t *testing.T) {
	port := reflect.TypeOf((*Claimer)(nil)).Elem()
	if port.NumMethod() != 2 {
		t.Fatalf("Claimer methods = %d, want exactly claim and release", port.NumMethod())
	}
	if _, ok := port.MethodByName("CompleteDispatchIntent"); ok {
		t.Fatal("Claimer exposes CompleteDispatchIntent")
	}
	if _, err := New(testConfig("inst_own"), &minimalClaimer{}, &staticLookup{}, &recordingBus{}); err != nil {
		t.Fatalf("New with a completion-free claim port: %v", err)
	}
}

func addSecondIntent(t *testing.T, claimer *recordingClaimer, lookup *staticLookup, instanceID core.ID, params map[string]any) *executionmodel.Execution {
	t.Helper()
	exec := testExecution(t, instanceID, params)
	intent := executionmodel.NewDispatchIntent(exec.ID, "cap", exec.AttemptID)
	claimer.mu.Lock()
	claimer.ready = append(claimer.ready, intent)
	claimer.mu.Unlock()
	lookup.mu.Lock()
	lookup.executions[exec.ID] = exec
	lookup.mu.Unlock()
	return exec
}

// TestReconcileOrderingIsReleaseBeforePublish uses one shared log so a publish
// before its release cannot be hidden by concatenating two separate histories.
func TestReconcileOrderingIsReleaseBeforePublish(t *testing.T) {
	r, claimer, lookup, bus, _, firstExec := testFixture(t, "inst_own")
	secondExec := addSecondIntent(t, claimer, lookup, "inst_own", map[string]any{"second": "value"})
	log := &operationLog{}
	claimer.log = log
	bus.log = log

	if err := r.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	want := []string{"claim", "release", "publish", "release", "publish"}
	if got := log.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("operation order = %v, want %v", got, want)
	}

	claims, releases, _, completes := claimer.snapshot()
	if len(claims) != 1 || len(releases) != 2 || completes != 0 {
		t.Fatalf("claim/release/complete = (%d,%d,%d), want (1,2,0)", len(claims), len(releases), completes)
	}
	for _, release := range releases {
		if release.owner != claims[0].owner {
			t.Errorf("release owner %q does not match pass token %q", release.owner, claims[0].owner)
		}
	}
	events, _ := bus.snapshot()
	if len(events) != 2 {
		t.Fatalf("published events = %d, want 2", len(events))
	}
	if events[0].Metadata.ExecutionID != firstExec.ID || events[1].Metadata.ExecutionID != secondExec.ID {
		t.Fatalf("published executions = (%s, %s), want (%s, %s)", events[0].Metadata.ExecutionID, events[1].Metadata.ExecutionID, firstExec.ID, secondExec.ID)
	}
	if releases[0].executionID != events[0].Metadata.ExecutionID || releases[1].executionID != events[1].Metadata.ExecutionID {
		t.Fatalf("release/publish pairs = (%s,%s),(%s,%s)", releases[0].executionID, events[0].Metadata.ExecutionID, releases[1].executionID, events[1].Metadata.ExecutionID)
	}
}

// TestReconcileStopsBatchOnForeignExecutionMismatch proves the invariant check
// aborts the whole pass: a foreign execution never causes a release or a publish,
// including for later members of the same claimed batch.
func TestReconcileStopsBatchOnForeignExecutionMismatch(t *testing.T) {
	r, claimer, lookup, bus, foreignIntent, _ := testFixture(t, "inst_own")
	foreignExec := testExecution(t, "inst_foreign", map[string]any{"secret": "foreign"})
	lookup.mu.Lock()
	lookup.executions[foreignIntent.ExecutionID] = foreignExec
	lookup.mu.Unlock()
	addSecondIntent(t, claimer, lookup, "inst_own", map[string]any{"own": "value"})

	if err := r.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	_, releases, operations, completes := claimer.snapshot()
	if len(releases) != 0 || completes != 0 {
		t.Fatalf("release/complete = (%d,%d), want (0,0)", len(releases), completes)
	}
	if len(operations) != 1 || operations[0] != "claim" {
		t.Fatalf("operations = %v, want claim only", operations)
	}
	events, busOperations := bus.snapshot()
	if len(events) != 0 || len(busOperations) != 0 {
		t.Fatalf("published events=%d on %v operations, want none", len(events), busOperations)
	}
	claimer.mu.Lock()
	defer claimer.mu.Unlock()
	for _, intent := range claimer.ready {
		if intent.Owner == "" {
			t.Fatalf("foreign mismatch released intent %+v", intent)
		}
	}
}

// TestReconcileLookupFailureReleasesAndContinues proves a failed durable lookup
// releases only the unreachable intent and does not block the rest of the batch.
func TestReconcileLookupFailureReleasesAndContinues(t *testing.T) {
	r, claimer, lookup, bus, failedIntent, _ := testFixture(t, "inst_own")
	secondExec := addSecondIntent(t, claimer, lookup, "inst_own", map[string]any{"second": "value"})
	lookup.failFor = failedIntent.ExecutionID
	log := &operationLog{}
	claimer.log = log
	bus.log = log
	var logs bytes.Buffer
	r.cfg.Logger = slog.New(slog.NewTextHandler(&logs, nil))

	if err := r.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	// The first release is failed-lookup cleanup; the second fences the healthy
	// intent immediately before its announcement.
	want := []string{"claim", "release", "release", "publish"}
	if got := log.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("operation order = %v, want %v", got, want)
	}
	_, releases, _, completes := claimer.snapshot()
	if len(releases) != 2 || releases[0].executionID != failedIntent.ExecutionID || completes != 0 {
		t.Fatalf("releases/completes = (%+v, %d), want only the failed lookup released", releases, completes)
	}
	events, _ := bus.snapshot()
	if len(events) != 1 || events[0].Metadata.ExecutionID != secondExec.ID {
		t.Fatalf("published events = %+v, want only %s", events, secondExec.ID)
	}
	if !strings.Contains(logs.String(), "load execution for dispatch reconciliation") {
		t.Fatalf("lookup failure was not logged: %q", logs.String())
	}
	claimer.mu.Lock()
	retryable := claimer.ready[0].Owner == "" && claimer.ready[0].LeaseExpiresAt == nil
	claimer.mu.Unlock()
	if !retryable {
		t.Fatal("failed lookup left its intent claimed")
	}
}

func TestRunWithPreCancelledContextDoesNoWork(t *testing.T) {
	r, claimer, _, bus, _, _ := testFixture(t, "inst_own")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run error = %v, want nil on cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return on an already-cancelled context")
	}
	claims, _, operations, _ := claimer.snapshot()
	events, busOperations := bus.snapshot()
	if len(claims) != 0 || len(operations) != 0 || len(events) != 0 || len(busOperations) != 0 {
		t.Fatalf("Run did work after Done: claims=%v operations=%v events=%d bus=%v", claims, operations, len(events), busOperations)
	}
}

// TestReconcileCancellationDuringReleaseSkipsPublish proves cancellation that
// lands mid-pass does not announce work after the shutdown signal. The claim was
// already released, so the next host can still discover the intent.
func TestReconcileCancellationDuringReleaseSkipsPublish(t *testing.T) {
	r, claimer, _, bus, _, _ := testFixture(t, "inst_own")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	claimer.onRelease = func() { cancel() }

	if err := r.Reconcile(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Reconcile error = %v, want context.Canceled", err)
	}
	events, busOperations := bus.snapshot()
	if len(events) != 0 || len(busOperations) != 0 {
		t.Fatalf("published after cancellation: events=%d operations=%v", len(events), busOperations)
	}
	_, releases, _, completes := claimer.snapshot()
	if len(releases) != 1 || completes != 0 {
		t.Fatalf("release/complete = (%d,%d), want (1,0)", len(releases), completes)
	}
}
