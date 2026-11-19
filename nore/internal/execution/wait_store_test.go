package execution

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/storage"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

// newStoredWaitExecution returns a store and a running execution with one
// capability already committed at revision 1.
func newStoredWaitExecution(t *testing.T, capabilityID shared.ID) (*ExecutionStore, *Execution, storage.Store) {
	t.Helper()
	raw := storage.NewMemoryStore()
	store := NewExecutionStore(raw)
	exec := newWaitExecution(t, capabilityID)
	if err := store.Add(context.Background(), exec); err != nil {
		t.Fatalf("Add: %v", err)
	}
	return store, exec, raw
}

func durableRevision(t *testing.T, store *ExecutionStore, id shared.ID) uint64 {
	t.Helper()
	key, err := store.key(id)
	if err != nil {
		t.Fatalf("execution key: %v", err)
	}
	data, err := store.store.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("read durable execution: %v", err)
	}
	var record struct {
		Revision uint64 `json:"revision"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("decode durable revision: %v", err)
	}
	return record.Revision
}

func waitTimerFixture(t *testing.T, exec *Execution, capabilityID, attemptID shared.ID, dueAt time.Time, payload []byte) (string, WaitTimerWrite) {
	t.Helper()
	key, err := WaitTimerKey(exec.ID, capabilityID, attemptID)
	if err != nil {
		t.Fatalf("WaitTimerKey: %v", err)
	}
	return key, WaitTimerWrite{Key: key, DueAt: dueAt, Data: payload}
}

func TestSaveWaitTransitionCommitsAllEffects(t *testing.T) {
	ctx := context.Background()
	capabilityID := shared.ID("cap_a")
	attemptID := shared.NewID("attempt_")
	store, exec, raw := newStoredWaitExecution(t, capabilityID)
	makeRunning(t, exec, capabilityID)

	until := time.Now().UTC().Add(time.Hour)
	if err := exec.EnterWait(capabilityID, signalWaitSpec(&until, WaitDeadlinePolicyFail)); err != nil {
		t.Fatalf("EnterWait: %v", err)
	}
	payload, err := EncodeWaitTimer(WaitTimerPayload{
		ExecutionID:  exec.ID,
		CapabilityID: capabilityID,
		WaitType:     WaitTypeSignal,
		AttemptID:    attemptID,
	})
	if err != nil {
		t.Fatalf("EncodeWaitTimer: %v", err)
	}
	timerKey, timerWrite := waitTimerFixture(t, exec, capabilityID, attemptID, until, payload)
	delivery, err := NewSignalDelivery(exec.ID, capabilityID, "del_1", "approval", "", map[string]any{"ok": true})
	if err != nil {
		t.Fatalf("NewSignalDelivery: %v", err)
	}
	intent := NewDispatchIntent(exec.ID, capabilityID, attemptID)

	result, err := store.SaveWaitTransition(ctx, WaitTransitionRequest{
		Execution: exec,
		Effects: WaitEffects{
			PutTimers: []WaitTimerWrite{timerWrite},
			Delivery:  delivery,
			Intents:   []*DispatchIntent{intent},
		},
	})
	if err != nil {
		t.Fatalf("SaveWaitTransition: %v", err)
	}
	if result.Replay {
		t.Fatal("first delivery reported as a replay")
	}
	if result.Revision != 2 || exec.Revision() != 2 || durableRevision(t, store, exec.ID) != 2 {
		t.Fatalf("revision = result %d, memory %d, durable %d; want 2", result.Revision, exec.Revision(), durableRevision(t, store, exec.ID))
	}
	if result.Delivery == nil || result.Delivery.DeliveryID != "del_1" {
		t.Fatalf("result delivery = %+v, want del_1", result.Delivery)
	}
	if intent.CreatedAt.IsZero() {
		t.Fatal("committed intent was not assigned a creation time")
	}

	storedTimer, err := raw.GetTimer(ctx, timerKey)
	if err != nil || string(storedTimer) != string(payload) {
		t.Fatalf("stored timer = %q, %v; want %q", storedTimer, err, payload)
	}
	deliveryKey, err := SignalDeliveryKey(exec.ID, "del_1")
	if err != nil {
		t.Fatalf("SignalDeliveryKey: %v", err)
	}
	storedDelivery, err := raw.Get(ctx, deliveryKey)
	if err != nil {
		t.Fatalf("read delivery: %v", err)
	}
	var decoded SignalDelivery
	if err := json.Unmarshal(storedDelivery, &decoded); err != nil {
		t.Fatalf("decode delivery: %v", err)
	}
	if decoded.CapabilityID != capabilityID || decoded.Status != SignalDeliveryAccepted {
		t.Fatalf("decoded delivery = %+v", decoded)
	}
	intentKey, err := dispatchIntentKey(exec.ID, capabilityID, attemptID)
	if err != nil {
		t.Fatalf("dispatchIntentKey: %v", err)
	}
	if _, err := raw.Get(ctx, intentKey); err != nil {
		t.Fatalf("read dispatch intent: %v", err)
	}
}

// failPutTimerTx fails every main-KV Put inside a real timer transaction so a
// later effect can be forced to abort after a timer write has already happened.
type failPutTimerTx struct {
	storage.TimerTx
}

func (tx *failPutTimerTx) Put(context.Context, string, []byte) error { return errInjected }

type failPutTimerStore struct {
	storage.Store
}

func (s *failPutTimerStore) WithTimer(ctx context.Context, fn func(storage.TimerTx) error) error {
	return s.Store.WithTimer(ctx, func(tx storage.TimerTx) error {
		return fn(&failPutTimerTx{TimerTx: tx})
	})
}

func TestSaveWaitTransitionCommitFailureLeavesNoPartialState(t *testing.T) {
	ctx := context.Background()
	capabilityID := shared.ID("cap_a")
	attemptID := shared.NewID("attempt_")
	raw := storage.NewMemoryStore()
	store := NewExecutionStore(&failPutTimerStore{Store: raw})
	exec := newWaitExecution(t, capabilityID)
	if err := store.Add(ctx, exec); err != nil {
		t.Fatalf("Add: %v", err)
	}

	payload, err := EncodeWaitTimer(WaitTimerPayload{ExecutionID: exec.ID, CapabilityID: capabilityID, AttemptID: attemptID})
	if err != nil {
		t.Fatalf("EncodeWaitTimer: %v", err)
	}
	_, timerWrite := waitTimerFixture(t, exec, capabilityID, attemptID, time.Now().UTC().Add(time.Hour), payload)
	delivery, err := NewSignalDelivery(exec.ID, capabilityID, "del_fail", "approval", "", map[string]any{"ok": true})
	if err != nil {
		t.Fatalf("NewSignalDelivery: %v", err)
	}
	intent := NewDispatchIntent(exec.ID, capabilityID, attemptID)

	_, err = store.SaveWaitTransition(ctx, WaitTransitionRequest{
		Execution: exec,
		Effects: WaitEffects{
			PutTimers: []WaitTimerWrite{timerWrite},
			Delivery:  delivery,
			Intents:   []*DispatchIntent{intent},
		},
	})
	if !errors.Is(err, errInjected) {
		t.Fatalf("SaveWaitTransition error = %v, want injected failure", err)
	}
	if exec.Revision() != 1 || durableRevision(t, store, exec.ID) != 1 {
		t.Fatalf("revision after failed commit = memory %d, durable %d; want 1", exec.Revision(), durableRevision(t, store, exec.ID))
	}
	if _, err := raw.GetTimer(ctx, timerWrite.Key); !errors.Is(err, storage.ErrTimerNotFound) {
		t.Fatalf("failed commit left a timer: %v", err)
	}
	deliveryKey, err := SignalDeliveryKey(exec.ID, "del_fail")
	if err != nil {
		t.Fatalf("SignalDeliveryKey: %v", err)
	}
	if _, err := raw.Get(ctx, deliveryKey); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("failed commit left a delivery: %v", err)
	}
	intentKey, err := dispatchIntentKey(exec.ID, capabilityID, attemptID)
	if err != nil {
		t.Fatalf("dispatchIntentKey: %v", err)
	}
	if _, err := raw.Get(ctx, intentKey); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("failed commit left a dispatch intent: %v", err)
	}
	if !intent.CreatedAt.IsZero() {
		t.Fatalf("failed commit mutated caller intent: %v", intent.CreatedAt)
	}
}

func TestSaveWaitTransitionDuplicateDeliveryIsIdempotent(t *testing.T) {
	ctx := context.Background()
	capabilityID := shared.ID("cap_a")
	firstAttempt := shared.NewID("attempt_")
	store, exec, raw := newStoredWaitExecution(t, capabilityID)

	firstDelivery, err := NewSignalDelivery(exec.ID, capabilityID, "del_1", "approval", "", map[string]any{"v": 1})
	if err != nil {
		t.Fatalf("NewSignalDelivery: %v", err)
	}
	firstPayload, err := EncodeWaitTimer(WaitTimerPayload{ExecutionID: exec.ID, CapabilityID: capabilityID, AttemptID: firstAttempt})
	if err != nil {
		t.Fatalf("EncodeWaitTimer: %v", err)
	}
	_, firstTimer := waitTimerFixture(t, exec, capabilityID, firstAttempt, time.Now().UTC().Add(time.Hour), firstPayload)
	if _, err := store.SaveWaitTransition(ctx, WaitTransitionRequest{
		Execution: exec,
		Effects: WaitEffects{
			PutTimers: []WaitTimerWrite{firstTimer},
			Delivery:  firstDelivery,
			Intents:   []*DispatchIntent{NewDispatchIntent(exec.ID, capabilityID, firstAttempt)},
		},
	}); err != nil {
		t.Fatalf("first SaveWaitTransition: %v", err)
	}

	secondAttempt := shared.NewID("attempt_")
	secondPayload, err := EncodeWaitTimer(WaitTimerPayload{ExecutionID: exec.ID, CapabilityID: capabilityID, AttemptID: secondAttempt})
	if err != nil {
		t.Fatalf("EncodeWaitTimer: %v", err)
	}
	_, secondTimer := waitTimerFixture(t, exec, capabilityID, secondAttempt, time.Now().UTC().Add(time.Hour), secondPayload)
	secondIntent := NewDispatchIntent(exec.ID, capabilityID, secondAttempt)
	replayDelivery, err := NewSignalDelivery(exec.ID, capabilityID, "del_1", "approval", "", map[string]any{"v": 1})
	if err != nil {
		t.Fatalf("NewSignalDelivery: %v", err)
	}
	replay, err := store.SaveWaitTransition(ctx, WaitTransitionRequest{
		Execution: exec,
		Effects: WaitEffects{
			PutTimers: []WaitTimerWrite{secondTimer},
			Delivery:  replayDelivery,
			Intents:   []*DispatchIntent{secondIntent},
		},
	})
	if err != nil {
		t.Fatalf("replay SaveWaitTransition: %v", err)
	}
	if !replay.Replay || replay.Delivery == nil || replay.Delivery.DeliveryID != "del_1" {
		t.Fatalf("replay result = %+v, want original delivery", replay)
	}
	if replay.Revision != 0 || exec.Revision() != 2 || durableRevision(t, store, exec.ID) != 2 {
		t.Fatalf("replay changed revision: result %d, memory %d, durable %d", replay.Revision, exec.Revision(), durableRevision(t, store, exec.ID))
	}
	if _, err := raw.GetTimer(ctx, secondTimer.Key); !errors.Is(err, storage.ErrTimerNotFound) {
		t.Fatalf("replay wrote a second timer: %v", err)
	}
	if !secondIntent.CreatedAt.IsZero() {
		t.Fatalf("replay mutated caller intent: %v", secondIntent.CreatedAt)
	}
	secondIntentKey, err := dispatchIntentKey(exec.ID, capabilityID, secondAttempt)
	if err != nil {
		t.Fatalf("dispatchIntentKey: %v", err)
	}
	if _, err := raw.Get(ctx, secondIntentKey); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("replay wrote a second intent: %v", err)
	}

	conflictDelivery, err := NewSignalDelivery(exec.ID, capabilityID, "del_1", "approval", "", map[string]any{"v": 2})
	if err != nil {
		t.Fatalf("NewSignalDelivery: %v", err)
	}
	_, err = store.SaveWaitTransition(ctx, WaitTransitionRequest{
		Execution: exec,
		Effects:   WaitEffects{Delivery: conflictDelivery},
	})
	if !errors.Is(err, ErrSignalDeliveryConflict) {
		t.Fatalf("conflicting payload error = %v, want ErrSignalDeliveryConflict", err)
	}
}

func TestSaveWaitTransitionTimerDeleteFailureModes(t *testing.T) {
	ctx := context.Background()
	capabilityID := shared.ID("cap_a")
	attemptID := shared.NewID("attempt_")

	t.Run("required missing timer", func(t *testing.T) {
		store, exec, _ := newStoredWaitExecution(t, capabilityID)
		_, err := store.SaveWaitTransition(ctx, WaitTransitionRequest{
			Execution: exec,
			Effects: WaitEffects{DeleteTimers: []WaitTimerDelete{{
				Key: "timers/missing", RequireExisting: true,
			}}},
		})
		if !errors.Is(err, ErrWaitTimerMissing) {
			t.Fatalf("error = %v, want ErrWaitTimerMissing", err)
		}
		if exec.Revision() != 1 || durableRevision(t, store, exec.ID) != 1 {
			t.Fatalf("failed commit changed revision: memory %d, durable %d", exec.Revision(), durableRevision(t, store, exec.ID))
		}
	})

	t.Run("optional missing timer", func(t *testing.T) {
		store, exec, _ := newStoredWaitExecution(t, capabilityID)
		result, err := store.SaveWaitTransition(ctx, WaitTransitionRequest{
			Execution: exec,
			Effects: WaitEffects{DeleteTimers: []WaitTimerDelete{{
				Key: "timers/missing",
			}}},
		})
		if err != nil || result.Revision != 2 {
			t.Fatalf("SaveWaitTransition = %+v, %v; want revision 2", result, err)
		}
	})

	t.Run("fenced delete reports lease loss", func(t *testing.T) {
		store, exec, raw := newStoredWaitExecution(t, capabilityID)
		key, err := WaitTimerKey(exec.ID, capabilityID, attemptID)
		if err != nil {
			t.Fatalf("WaitTimerKey: %v", err)
		}
		if err := raw.PutTimer(ctx, key, time.Now().UTC().Add(-time.Minute), []byte("timer")); err != nil {
			t.Fatalf("PutTimer: %v", err)
		}
		claimed, err := raw.ClaimTimer(ctx, key, "owner_1", time.Hour)
		if err != nil || !claimed {
			t.Fatalf("ClaimTimer = %v, %v", claimed, err)
		}

		_, err = store.SaveWaitTransition(ctx, WaitTransitionRequest{
			Execution: exec,
			Effects: WaitEffects{DeleteTimers: []WaitTimerDelete{{
				Key: key, Owner: "owner_2",
			}}},
		})
		if !errors.Is(err, ErrWaitTimerLeaseLost) {
			t.Fatalf("error = %v, want ErrWaitTimerLeaseLost", err)
		}
		if exec.Revision() != 1 {
			t.Fatalf("lease-lost commit changed revision to %d", exec.Revision())
		}

		result, err := store.SaveWaitTransition(ctx, WaitTransitionRequest{
			Execution: exec,
			Effects: WaitEffects{DeleteTimers: []WaitTimerDelete{{
				Key: key, Owner: "owner_1",
			}}},
		})
		if err != nil || result.Revision != 2 {
			t.Fatalf("owner commit = %+v, %v; want revision 2", result, err)
		}
		if _, err := raw.GetTimer(ctx, key); !errors.Is(err, storage.ErrTimerNotFound) {
			t.Fatalf("owned timer still present: %v", err)
		}
	})
}

func TestWaitTimerFencingOperations(t *testing.T) {
	ctx := context.Background()
	raw := storage.NewMemoryStore()
	store := NewExecutionStore(raw)
	key := "timers/fencing/cap/attempt"
	if err := raw.PutTimer(ctx, key, time.Now().UTC().Add(-time.Minute), []byte("timer")); err != nil {
		t.Fatalf("PutTimer: %v", err)
	}

	claimed, err := store.ClaimWaitTimer(ctx, key, "owner_1", time.Hour)
	if err != nil || !claimed {
		t.Fatalf("ClaimWaitTimer = %v, %v", claimed, err)
	}
	if err := store.RenewWaitTimer(ctx, key, "owner_1", time.Hour); err != nil {
		t.Fatalf("RenewWaitTimer owner: %v", err)
	}
	if err := store.RenewWaitTimer(ctx, key, "owner_2", time.Hour); !errors.Is(err, ErrWaitTimerLeaseLost) {
		t.Fatalf("renew by other owner = %v, want ErrWaitTimerLeaseLost", err)
	}
	if deleted, err := store.DeleteWaitTimerOwned(ctx, key, "owner_2"); err != nil || deleted {
		t.Fatalf("delete by other owner = %v, %v; want false", deleted, err)
	}
	if deleted, err := store.DeleteWaitTimerOwned(ctx, key, "owner_1"); err != nil || !deleted {
		t.Fatalf("delete by owner = %v, %v; want true", deleted, err)
	}
	if deleted, err := store.DeleteWaitTimerOwned(ctx, key, "owner_1"); err != nil || deleted {
		t.Fatalf("second delete = %v, %v; want false", deleted, err)
	}

	if _, err := store.ClaimWaitTimer(ctx, key, "", time.Minute); !errors.Is(err, storage.ErrTimerOwnerEmpty) {
		t.Fatalf("empty claim owner = %v, want ErrTimerOwnerEmpty", err)
	}
	if _, err := store.ClaimWaitTimer(ctx, key, "owner_1", 0); err == nil {
		t.Fatal("non-positive lease was accepted")
	}
	if err := store.RenewWaitTimer(ctx, key, "", time.Minute); !errors.Is(err, storage.ErrTimerOwnerEmpty) {
		t.Fatalf("empty renew owner = %v, want ErrTimerOwnerEmpty", err)
	}
	if _, err := store.DeleteWaitTimerOwned(ctx, key, ""); !errors.Is(err, storage.ErrTimerOwnerEmpty) {
		t.Fatalf("empty delete owner = %v, want ErrTimerOwnerEmpty", err)
	}
}

func TestWaitKeysAndPayloadEncodeDecode(t *testing.T) {
	key, err := WaitTimerKey("exec_1", "cap_1", "attempt_1")
	if err != nil || key != "timers/exec_1/cap_1/attempt_1" {
		t.Fatalf("WaitTimerKey = %q, %v", key, err)
	}
	if _, err := WaitTimerKey("exec_1", "cap_1", ""); err == nil {
		t.Fatal("wait timer key accepted an empty attempt ID")
	}
	if _, err := SignalDeliveryKey("exec_1", ""); err == nil {
		t.Fatal("delivery key accepted an empty ID")
	}
	if _, err := SignalDeliveryKey("exec_1", "../escape"); !errors.Is(err, storage.ErrInvalidKey) {
		t.Fatalf("traversal delivery key error = %v, want ErrInvalidKey", err)
	}

	encoded, err := EncodeWaitTimer(WaitTimerPayload{
		ExecutionID: "exec_1", CapabilityID: "cap_1", WaitType: WaitTypeTimer, AttemptID: "attempt_1",
	})
	if err != nil {
		t.Fatalf("EncodeWaitTimer: %v", err)
	}
	decoded, err := DecodeWaitTimer(encoded)
	if err != nil {
		t.Fatalf("DecodeWaitTimer: %v", err)
	}
	if decoded.ExecutionID != "exec_1" || decoded.CapabilityID != "cap_1" || decoded.WaitType != WaitTypeTimer || decoded.AttemptID != "attempt_1" {
		t.Fatalf("decoded payload = %+v", decoded)
	}
	if _, err := DecodeWaitTimer([]byte("{not json")); err == nil {
		t.Fatal("malformed timer payload was accepted")
	}
	if _, err := DecodeWaitTimer([]byte(`{"wait_type":"timer"}`)); err == nil {
		t.Fatal("timer payload without IDs was accepted")
	}
}

func TestSignalDeliveryPayloadHashIsCanonical(t *testing.T) {
	first, err := NewSignalDelivery("exec_1", "cap_1", "del_1", "approval", "", map[string]any{"a": 1, "b": 2})
	if err != nil {
		t.Fatalf("NewSignalDelivery: %v", err)
	}
	second, err := NewSignalDelivery("exec_1", "cap_1", "del_1", "approval", "", map[string]any{"b": 2, "a": 1})
	if err != nil {
		t.Fatalf("NewSignalDelivery: %v", err)
	}
	if first.PayloadHash != second.PayloadHash {
		t.Fatalf("payload hash depends on map order: %q vs %q", first.PayloadHash, second.PayloadHash)
	}
	changed, err := NewSignalDelivery("exec_1", "cap_1", "del_1", "approval", "", map[string]any{"a": 2, "b": 2})
	if err != nil {
		t.Fatalf("NewSignalDelivery: %v", err)
	}
	if changed.PayloadHash == first.PayloadHash {
		t.Fatal("different payloads produced the same hash")
	}
	if _, err := NewSignalDelivery("exec_1", "cap_1", "", "approval", "", nil); err == nil {
		t.Fatal("delivery without an ID was accepted")
	}
}
