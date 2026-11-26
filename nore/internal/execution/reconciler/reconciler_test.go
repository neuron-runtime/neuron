package reconciler

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/event"
	executionmodel "github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/nore/internal/types"
	core "github.com/neuron-runtime/neuron/shared/types/core"
)

type claimCall struct {
	instanceID core.ID
	owner      string
	lease      time.Duration
	limit      int
}

type releaseCall struct {
	executionID  core.ID
	capabilityID core.ID
	attemptID    core.ID
	owner        string
}

// recordingClaimer implements the consumer-local claim port. It keeps intent
// ownership in memory so a released intent can be claimed again on the next
// pass, which is what proves a failed publish stays retryable.
type recordingClaimer struct {
	mu sync.Mutex

	ready      []*executionmodel.DispatchIntent
	claims     []claimCall
	releases   []releaseCall
	operations []string
	claimErr   error
	releaseErr error
	onClaim    func()
	onRelease  func()
	log        *operationLog

	// completeCalls exists only to prove the reconciler never completes an
	// intent. CompleteDispatchIntent is not part of Claimer, so a reconciler
	// that called it could not compile; the counter documents the invariant.
	completeCalls int
}

func (c *recordingClaimer) ClaimReadyDispatchIntents(_ context.Context, instanceID core.ID, owner string, lease time.Duration, limit int) ([]*executionmodel.DispatchIntent, error) {
	c.mu.Lock()
	c.claims = append(c.claims, claimCall{instanceID: instanceID, owner: owner, lease: lease, limit: limit})
	c.operations = append(c.operations, "claim")
	c.log.add("claim")
	onClaim := c.onClaim
	err := c.claimErr
	defer c.mu.Unlock()
	if onClaim != nil {
		onClaim()
	}
	if err != nil {
		return nil, err
	}

	claimed := make([]*executionmodel.DispatchIntent, 0, limit)
	expires := time.Now().UTC().Add(lease)
	for _, intent := range c.ready {
		if len(claimed) >= limit {
			break
		}
		if intent.Owner != "" && intent.LeaseExpiresAt != nil && intent.LeaseExpiresAt.After(time.Now().UTC()) {
			continue
		}
		intent.Owner = owner
		intent.LeaseExpiresAt = &expires
		copy := *intent
		claimed = append(claimed, &copy)
	}
	return claimed, nil
}

func (c *recordingClaimer) ReleaseDispatchIntent(_ context.Context, executionID, capabilityID, attemptID core.ID, owner string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.releases = append(c.releases, releaseCall{executionID: executionID, capabilityID: capabilityID, attemptID: attemptID, owner: owner})
	c.operations = append(c.operations, "release")
	c.log.add("release")
	if c.onRelease != nil {
		c.onRelease()
	}
	if c.releaseErr != nil {
		return c.releaseErr
	}
	for _, intent := range c.ready {
		if intent.ExecutionID == executionID && intent.CapabilityID == capabilityID && intent.AttemptID == attemptID {
			intent.Owner = ""
			intent.LeaseExpiresAt = nil
		}
	}
	return nil
}

func (c *recordingClaimer) CompleteDispatchIntent(context.Context, core.ID, core.ID, core.ID, string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.completeCalls++
	return nil
}

func (c *recordingClaimer) snapshot() ([]claimCall, []releaseCall, []string, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]claimCall(nil), c.claims...), append([]releaseCall(nil), c.releases...), append([]string(nil), c.operations...), c.completeCalls
}

type staticLookup struct {
	mu         sync.Mutex
	executions map[core.ID]*executionmodel.Execution
	err        error
	failFor    core.ID
}

func (l *staticLookup) Get(_ context.Context, executionID core.ID) (*executionmodel.Execution, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return nil, l.err
	}
	if l.failFor != "" && executionID == l.failFor {
		return nil, errors.New("injected lookup failure")
	}
	exec, ok := l.executions[executionID]
	if !ok {
		return nil, errors.New("execution not found")
	}
	return exec, nil
}

type recordingBus struct {
	mu         sync.Mutex
	events     []event.Event
	operations []string
	failures   int
	err        error
	log        *operationLog
}

func (b *recordingBus) Publish(_ context.Context, evt event.Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.operations = append(b.operations, "publish")
	b.log.add("publish")
	if b.failures > 0 {
		b.failures--
		if b.err != nil {
			return b.err
		}
		return errors.New("publish unavailable")
	}
	if b.err != nil {
		return b.err
	}
	b.events = append(b.events, evt)
	return nil
}

func (*recordingBus) Subscribe(event.Type, int) (event.Subscription, error) {
	return nil, errors.New("recording bus does not subscribe")
}

func (*recordingBus) Close() error { return nil }

func (b *recordingBus) snapshot() ([]event.Event, []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]event.Event(nil), b.events...), append([]string(nil), b.operations...)
}

func testConfig(instanceID core.ID) Config {
	return Config{
		InstanceID: instanceID,
		Interval:   time.Second,
		BatchSize:  8,
		Lease:      30 * time.Second,
		Logger:     slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
	}
}

func testExecution(t *testing.T, instanceID core.ID, params map[string]any) *executionmodel.Execution {
	t.Helper()
	blueprint := &types.ExecutionBlueprint{Nodes: map[core.ID]types.ExecutionNode{"cap": {}}}
	exec, err := executionmodel.NewExecution(blueprint, core.NewID("corr_"), instanceID, "", "", "", "")
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	if err := exec.Start(params, 1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := exec.MarkCapabilityReady("cap", params); err != nil {
		t.Fatalf("MarkCapabilityReady: %v", err)
	}
	return exec
}

func testFixture(t *testing.T, instanceID core.ID) (*Reconciler, *recordingClaimer, *staticLookup, *recordingBus, *executionmodel.DispatchIntent, *executionmodel.Execution) {
	t.Helper()
	exec := testExecution(t, instanceID, map[string]any{"seed": "value"})
	intent := executionmodel.NewDispatchIntent(exec.ID, "cap", exec.AttemptID)
	claimer := &recordingClaimer{ready: []*executionmodel.DispatchIntent{intent}}
	lookup := &staticLookup{executions: map[core.ID]*executionmodel.Execution{exec.ID: exec}}
	bus := &recordingBus{}
	r, err := New(testConfig(instanceID), claimer, lookup, bus)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r, claimer, lookup, bus, intent, exec
}

func TestNewValidatesConfigurationAndPorts(t *testing.T) {
	valid := testConfig("inst_own")
	bus := &recordingBus{}
	claimer := &recordingClaimer{}
	lookup := &staticLookup{}

	cases := []struct {
		name string
		cfg  Config
		err  string
	}{
		{"empty instance", func() Config { c := valid; c.InstanceID = ""; return c }(), "instance ID"},
		{"zero interval", func() Config { c := valid; c.Interval = 0; return c }(), "interval"},
		{"negative batch", func() Config { c := valid; c.BatchSize = -1; return c }(), "batch size"},
		{"zero lease", func() Config { c := valid; c.Lease = 0; return c }(), "lease"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.cfg, claimer, lookup, bus); err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Fatalf("New error = %v, want %q", err, tc.err)
			}
		})
	}
	if _, err := New(valid, nil, lookup, bus); err == nil {
		t.Fatal("New accepted a nil claim port")
	}
	if _, err := New(valid, claimer, nil, bus); err == nil {
		t.Fatal("New accepted a nil execution lookup")
	}
	if _, err := New(valid, claimer, lookup, nil); err == nil {
		t.Fatal("New accepted a nil bus")
	}
	r, err := New(valid, claimer, lookup, bus)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if r.Config().Logger == nil {
		t.Fatal("New did not default the logger")
	}
}

// TestReconcileUsesFreshBoundedTokenPerPass proves that each pass carries a new
// entropy-backed owner with a stable diagnostic identity and forwards the real
// instance ID, lease, and batch bound to the store primitive.
func TestReconcileUsesFreshBoundedTokenPerPass(t *testing.T) {
	r, claimer, _, _, _, _ := testFixture(t, "inst_own")
	for pass := 0; pass < 3; pass++ {
		if err := r.Reconcile(context.Background()); err != nil {
			t.Fatalf("Reconcile %d: %v", pass, err)
		}
	}
	claims, _, _, _ := claimer.snapshot()
	if len(claims) != 3 {
		t.Fatalf("claim calls = %d, want 3", len(claims))
	}
	seen := make(map[string]bool, len(claims))
	for _, call := range claims {
		if call.instanceID != "inst_own" {
			t.Errorf("claimed for %q, want inst_own", call.instanceID)
		}
		if call.lease != r.Config().Lease || call.limit != r.Config().BatchSize {
			t.Errorf("claim bounds = (%s, %d), want (%s, %d)", call.lease, call.limit, r.Config().Lease, r.Config().BatchSize)
		}
		prefix := "inst_own/reconciler/claim_"
		if !strings.HasPrefix(call.owner, prefix) {
			t.Errorf("owner %q lacks stable prefix %q", call.owner, prefix)
		}
		if len(call.owner) <= len(prefix) {
			t.Errorf("owner %q has no fresh ID suffix", call.owner)
		}
		if seen[call.owner] {
			t.Errorf("owner token %q was reused across passes", call.owner)
		}
		seen[call.owner] = true
	}
}

// TestReconcileReleasesBeforePublishAndNeverCompletes proves the ordering and
// the event shape with a recording committer and bus at the reconciler boundary.
func TestReconcileReleasesBeforePublishAndNeverCompletes(t *testing.T) {
	r, claimer, _, bus, intent, exec := testFixture(t, "inst_own")
	if err := r.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	_, releases, operations, completes := claimer.snapshot()
	_, busOperations := bus.snapshot()
	combined := append(append([]string(nil), operations...), busOperations...)
	want := []string{"claim", "release", "publish"}
	if len(combined) != len(want) {
		t.Fatalf("reconciler operations = %v, want %v", combined, want)
	}
	if len(busOperations) != 1 || len(releases) != 1 {
		t.Fatalf("release/publish counts = (%d, %d), want (1, 1)", len(releases), len(busOperations))
	}
	if combined[0] != "claim" || combined[1] != "release" || combined[2] != "publish" {
		t.Fatalf("operation order = %v, want claim, release before publish", combined)
	}
	if completes != 0 {
		t.Fatalf("CompleteDispatchIntent calls = %d, want 0", completes)
	}
	if releases[0].owner == "" || !strings.HasPrefix(releases[0].owner, "inst_own/reconciler/claim_") {
		t.Fatalf("release owner = %q, want the pass token", releases[0].owner)
	}
	if releases[0].executionID != intent.ExecutionID || releases[0].capabilityID != intent.CapabilityID || releases[0].attemptID != intent.AttemptID {
		t.Fatalf("release identity = %+v, want %+v", releases[0], intent)
	}

	events, _ := bus.snapshot()
	if len(events) != 1 {
		t.Fatalf("published events = %d, want 1", len(events))
	}
	got := events[0]
	if got.Type != event.CapabilityReady || got.Metadata.ExecutionID != exec.ID || got.Metadata.CorrelationID != exec.CorrelationID || got.Metadata.CapabilityID != "cap" {
		t.Fatalf("published event = %+v, want CapabilityReady for cap on %s", got, exec.ID)
	}
	payload, ok := got.Payload.(event.CapabilityReadyPayload)
	if !ok {
		t.Fatalf("payload type = %T, want event.CapabilityReadyPayload", got.Payload)
	}
	if payload.Params["seed"] != "value" {
		t.Fatalf("payload params = %v, want execution params", payload.Params)
	}
}

// TestReconcilePublishFailureLeavesIntentClaimable proves a failed announcement
// does not complete or strand the intent: the claim was released first, so the
// next pass claims it under a fresh token and republishes it.
func TestReconcilePublishFailureLeavesIntentClaimable(t *testing.T) {
	r, claimer, _, bus, intent, _ := testFixture(t, "inst_own")
	bus.failures = 1

	if err := r.Reconcile(context.Background()); err != nil {
		t.Fatalf("first Reconcile: %v", err)
	}
	events, operations := bus.snapshot()
	if len(events) != 0 || len(operations) != 1 {
		t.Fatalf("first pass events/operations = (%d, %v), want no event and one publish attempt", len(events), operations)
	}
	claims, releases, _, completes := claimer.snapshot()
	if len(claims) != 1 || len(releases) != 1 || completes != 0 {
		t.Fatalf("first pass claim/release/complete = (%d, %d, %d), want (1, 1, 0)", len(claims), len(releases), completes)
	}
	if claimer.ready[0].Owner != "" || claimer.ready[0].LeaseExpiresAt != nil {
		t.Fatalf("failed publish left the intent claimed: %+v", claimer.ready[0])
	}

	if err := r.Reconcile(context.Background()); err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	claims, _, _, completes = claimer.snapshot()
	events, operations = bus.snapshot()
	if len(claims) != 2 || claims[0].owner == claims[1].owner {
		t.Fatalf("second pass did not use a fresh token: %+v", claims)
	}
	if len(events) != 1 || len(operations) != 2 {
		t.Fatalf("second pass events/operations = (%d, %v), want one event after two publish attempts", len(events), operations)
	}
	if events[0].Metadata.ExecutionID != intent.ExecutionID {
		t.Fatalf("republished execution = %s, want %s", events[0].Metadata.ExecutionID, intent.ExecutionID)
	}
	if completes != 0 {
		t.Fatalf("CompleteDispatchIntent calls = %d, want 0", completes)
	}
}

// TestReconcileReleaseFailureSkipsPublish proves a failed fence release logs the
// failure and never announces work that may still be owned by the pass.
func TestReconcileReleaseFailureSkipsPublish(t *testing.T) {
	r, claimer, _, bus, _, _ := testFixture(t, "inst_own")
	claimer.releaseErr = errors.New("release unavailable")
	var logs bytes.Buffer
	r.cfg.Logger = slog.New(slog.NewTextHandler(&logs, nil))

	if err := r.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	events, operations := bus.snapshot()
	if len(events) != 0 || len(operations) != 0 {
		t.Fatalf("publish after release failure: events=%d operations=%v", len(events), operations)
	}
	if !strings.Contains(logs.String(), "release dispatch claim") {
		t.Fatalf("release failure was not logged: %q", logs.String())
	}
}

// TestReconcileStopsOnForeignExecutionInvariant proves that a stale claim whose
// execution belongs to another instance is neither released nor published: the
// store's pre-CAS filter is the only foreign-filter authority.
func TestReconcileStopsOnForeignExecutionInvariant(t *testing.T) {
	r, claimer, lookup, bus, _, _ := testFixture(t, "inst_own")
	foreign := testExecution(t, "inst_foreign", map[string]any{"secret": "foreign"})
	lookup.mu.Lock()
	lookup.executions[claimer.ready[0].ExecutionID] = foreign
	lookup.mu.Unlock()

	if err := r.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	claims, releases, operations, completes := claimer.snapshot()
	if len(claims) != 1 || len(releases) != 0 || completes != 0 {
		t.Fatalf("foreign invariant claim/release/complete = (%d, %d, %d), want (1, 0, 0)", len(claims), len(releases), completes)
	}
	if len(operations) != 1 || operations[0] != "claim" {
		t.Fatalf("foreign invariant operations = %v, want claim only", operations)
	}
	events, busOperations := bus.snapshot()
	if len(events) != 0 || len(busOperations) != 0 {
		t.Fatalf("foreign invariant published %d events on %v operations", len(events), busOperations)
	}
	if claimer.ready[0].Owner == "" {
		t.Fatal("foreign mismatch mutated the claimed intent")
	}
}

func TestReconcileStopsWithCancelledContextBeforeClaim(t *testing.T) {
	r, claimer, _, bus, _, _ := testFixture(t, "inst_own")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := r.Reconcile(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Reconcile error = %v, want context.Canceled", err)
	}
	claims, _, operations, _ := claimer.snapshot()
	events, _ := bus.snapshot()
	if len(claims) != 0 || len(operations) != 0 || len(events) != 0 {
		t.Fatalf("cancelled pass did work: claims=%v operations=%v events=%d", claims, operations, len(events))
	}
}

// TestRunIsPeriodicAndStopsOnCancellation proves Run waits for the first tick,
// performs one pass per tick, and stops taking work once its context is done.
func TestRunIsPeriodicAndStopsOnCancellation(t *testing.T) {
	r, claimer, _, _, _, _ := testFixture(t, "inst_own")
	r.cfg.Interval = 100 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var stopOnce sync.Once
	claimer.onClaim = func() { stopOnce.Do(cancel) }
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()

	time.Sleep(30 * time.Millisecond)
	claims, _, _, _ := claimer.snapshot()
	if len(claims) != 0 {
		t.Fatalf("Run claimed before the first tick: %v", claims)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run error = %v, want nil on cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after cancellation")
	}
	claimsAfter, _, _, _ := claimer.snapshot()
	if len(claimsAfter) != 1 {
		t.Fatalf("claim passes before stop = %d, want 1", len(claimsAfter))
	}
	time.Sleep(3 * r.Config().Interval)
	claimsQuiet, _, _, _ := claimer.snapshot()
	if len(claimsQuiet) != len(claimsAfter) {
		t.Fatalf("Run claimed after cancellation: %d -> %d", len(claimsAfter), len(claimsQuiet))
	}
}
