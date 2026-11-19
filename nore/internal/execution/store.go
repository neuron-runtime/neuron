package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/data"
	"github.com/neuron-runtime/neuron/nore/internal/storage"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

var (
	// ErrSignalDeliveryConflict reports a repeated delivery ID carrying a
	// different payload. The original delivery record is left untouched.
	ErrSignalDeliveryConflict = errors.New("execution: signal delivery id already used with a different payload")

	// ErrWaitTimerLeaseLost reports that a claimed wait timer was no longer
	// owned by the caller when the transition tried to fence it. The transition
	// is not committed.
	ErrWaitTimerLeaseLost = errors.New("execution: wait timer lease lost")

	// ErrWaitTimerMissing reports that a required wait timer did not exist when
	// the transition was committed. The transition is not committed.
	ErrWaitTimerMissing = errors.New("execution: wait timer missing")
)

// ExecutionStore is the durable execution repository. Memory is a read-through
// cache for live executions; durable state is authoritative for everything not
// currently live.
//
// Concurrency contract: Save serializes writes for one Execution value with a
// per-execution mutex, then compares the snapshot revision with the stored
// revision inside one transaction. Composite transitions on a shared Execution
// value are serialized by Execution.RunTransition, so same-value writers cannot
// race between reading state and committing it, nor produce a false conflict.
// The revision compare-and-swap is the backstop for a distinct stale copy: a
// conflict means another store or process holds an older copy. That distinction
// is why the revision guard is meaningful instead of merely rejecting ordinary
// same-process races.
type ExecutionStore struct {
	mem   *MemoryStore
	store storage.Store
}

func NewExecutionStore(store storage.Store) *ExecutionStore {
	return &ExecutionStore{mem: NewMemoryStore(), store: store}
}

// Add persists a new execution and only then inserts it into memory. It is
// insert-only: an existing durable record makes Add fail with
// storage.ErrAlreadyExists rather than overwriting a previous execution.
func (s *ExecutionStore) Add(ctx context.Context, execution *Execution) error {
	if execution == nil {
		return fmt.Errorf("execution is required")
	}
	execution.saveMu.Lock()
	defer execution.saveMu.Unlock()

	key, err := s.key(execution.ID)
	if err != nil {
		return err
	}
	data, err := marshalExecutionAt(execution, 1)
	if err != nil {
		return fmt.Errorf("marshal execution: %w", err)
	}

	err = s.store.WithTx(ctx, func(tx storage.Tx) error {
		if _, err := tx.Get(ctx, key); err == nil {
			return fmt.Errorf("%w: execution %s", storage.ErrAlreadyExists, execution.ID)
		} else if !errors.Is(err, storage.ErrNotFound) {
			return fmt.Errorf("read execution %s: %w", execution.ID, err)
		}
		return tx.Put(ctx, key, data)
	})
	if err != nil {
		return fmt.Errorf("persist execution %s: %w", execution.ID, err)
	}

	if err := s.mem.Add(ctx, execution); err != nil {
		// Memory refused the execution after the durable write succeeded. Remove
		// the durable record so a later restart does not surface an execution the
		// live store never accepted.
		if cleanupErr := s.store.Delete(ctx, key); cleanupErr != nil {
			return errors.Join(err, fmt.Errorf("remove unaccepted execution %s: %w", execution.ID, cleanupErr))
		}
		return err
	}

	execution.setRevision(1)
	return nil
}

// Get returns the live execution when present, otherwise the durable record. A
// missing execution reports storage.ErrNotFound; a backend or decode failure is
// returned as an error and never converted into absence.
func (s *ExecutionStore) Get(ctx context.Context, executionID shared.ID) (*Execution, error) {
	exec, err := s.mem.Get(ctx, executionID)
	if err == nil {
		return exec, nil
	}
	if !errors.Is(err, storage.ErrNotFound) {
		return nil, err
	}
	return s.loadFromStore(ctx, executionID)
}

// Save persists execution's current snapshot with revision compare-and-swap and
// only then records it in memory. The snapshot revision is incremented on
// success. A stale revision reports storage.ErrConflict and leaves both durable
// and in-memory state untouched.
//
// Persisting before the memory update is deliberate: a caller must never observe
// an execution as live that durable storage rejected.
func (s *ExecutionStore) Save(ctx context.Context, execution *Execution) error {
	if execution == nil {
		return fmt.Errorf("execution is required")
	}
	execution.saveMu.Lock()
	defer execution.saveMu.Unlock()

	key, err := s.key(execution.ID)
	if err != nil {
		return err
	}

	var nextRevision uint64
	err = s.store.WithTx(ctx, func(tx storage.Tx) error {
		next, err := writeExecutionRevision(ctx, tx, key, execution)
		if err != nil {
			return err
		}
		nextRevision = next
		return nil
	})
	if err != nil {
		return fmt.Errorf("persist execution %s: %w", execution.ID, err)
	}

	execution.setRevision(nextRevision)
	if err := s.mem.Save(ctx, execution); err != nil {
		return fmt.Errorf("record execution %s in memory after durable save: %w", execution.ID, err)
	}
	return nil
}

// SaveTransition atomically persists an execution's next revision together with
// the dispatch intents produced by that state transition.
//
// A successful return is the commit-ownership boundary: the execution snapshot
// and every provided intent are durably committed, not merely updated in memory.
// If any write fails the transaction rolls back, so a caller never observes a
// transition whose intents were lost, or intents whose transition was lost.
// Intents are written directly through the transaction; SaveTransition must not
// call EnqueueDispatchIntent because that would open a nested transaction.
//
// The revision contract matches Save: a stale execution reports
// storage.ErrConflict and leaves both durable and in-memory state untouched.
func (s *ExecutionStore) SaveTransition(ctx context.Context, execution *Execution, intents []*DispatchIntent) error {
	if execution == nil {
		return fmt.Errorf("execution is required")
	}
	execution.saveMu.Lock()
	defer execution.saveMu.Unlock()

	key, err := s.key(execution.ID)
	if err != nil {
		return err
	}

	// Resolve every intent key before opening the transaction so invalid input
	// fails fast. CreatedAt is applied to copies and only written back to the
	// caller's intents after the commit, so a rolled-back transition does not
	// mutate them.
	pending, err := prepareDispatchIntents(intents)
	if err != nil {
		return err
	}

	var nextRevision uint64
	createdAt := time.Now().UTC()
	err = s.store.WithTx(ctx, func(tx storage.Tx) error {
		next, err := writeExecutionRevision(ctx, tx, key, execution)
		if err != nil {
			return err
		}
		nextRevision = next
		return writeDispatchIntents(ctx, tx, pending, createdAt)
	})
	if err != nil {
		return fmt.Errorf("persist execution %s transition: %w", execution.ID, err)
	}

	execution.setRevision(nextRevision)
	applyDispatchIntentTimestamps(intents, pending)
	if err := s.mem.Save(ctx, execution); err != nil {
		return fmt.Errorf("record execution %s in memory after durable transition: %w", execution.ID, err)
	}
	return nil
}

// Adopt records an execution as live without writing it back to durable
// storage. Recovery uses it after loading a snapshot and attaching its
// recompiled blueprint: the scheduler and engine must share one Execution value,
// and a fresh Get would otherwise load a second copy from storage.
//
// Adopt deliberately does not change the revision. The snapshot it is given is
// the durable state the caller just read, so the next transition compares
// against that revision exactly as it would after a normal load.
func (s *ExecutionStore) Adopt(ctx context.Context, execution *Execution) error {
	if execution == nil {
		return fmt.Errorf("execution is required")
	}
	if err := s.mem.Save(ctx, execution); err != nil {
		return fmt.Errorf("adopt execution %s: %w", execution.ID, err)
	}
	return nil
}

// ClearDispatchIntents removes every dispatch intent belonging to execution.
//
// Recovery calls it before re-deriving intents from the recovered state. A
// crashed process can leave an intent claimed under a lease that has not yet
// expired, or an intent for a capability that has since completed; neither is
// valid work, and the lease would otherwise delay or duplicate dispatch.
func (s *ExecutionStore) ClearDispatchIntents(ctx context.Context, executionID shared.ID) error {
	prefix, err := storage.Prefix("dispatch_intents", string(executionID))
	if err != nil {
		return fmt.Errorf("prefix dispatch intents for execution %s: %w", executionID, err)
	}
	return s.store.WithTx(ctx, func(tx storage.Tx) error {
		keys, err := tx.List(ctx, prefix)
		if err != nil {
			return fmt.Errorf("list dispatch intents for execution %s: %w", executionID, err)
		}
		for _, key := range keys {
			if err := tx.Delete(ctx, key); err != nil {
				return fmt.Errorf("delete dispatch intent %s: %w", key, err)
			}
		}
		return nil
	})
}

// Delete removes the durable record before the in-memory record. A failed
// durable delete leaves memory intact so the execution remains readable.
func (s *ExecutionStore) Delete(ctx context.Context, executionID shared.ID) error {
	key, err := s.key(executionID)
	if err != nil {
		return err
	}
	if err := s.store.Delete(ctx, key); err != nil {
		return fmt.Errorf("delete execution %s: %w", executionID, err)
	}
	if err := s.mem.Delete(ctx, executionID); err != nil {
		return fmt.Errorf("remove execution %s from memory: %w", executionID, err)
	}
	return nil
}

// List merges live executions with durable records. A storage or decode failure
// is returned; it is never replaced with a partial in-memory list.
func (s *ExecutionStore) List(ctx context.Context) ([]*Execution, error) {
	prefix, err := storage.Prefix("executions")
	if err != nil {
		return nil, err
	}
	keys, err := s.store.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("list executions: %w", err)
	}

	result := make([]*Execution, 0, len(keys))
	seen := make(map[shared.ID]bool)

	live, err := s.mem.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, exec := range live {
		result = append(result, exec)
		seen[exec.ID] = true
	}

	for _, key := range keys {
		id, err := executionIDFromKey(key)
		if err != nil {
			return nil, err
		}
		if seen[id] {
			continue
		}
		exec, err := s.loadFromStore(ctx, id)
		if errors.Is(err, storage.ErrNotFound) {
			// The record was deleted between List and Get.
			continue
		}
		if err != nil {
			return nil, err
		}
		result = append(result, exec)
		seen[id] = true
	}

	return result, nil
}

// ReferencedArtifactHashes returns the assembly artifact hashes pinned by
// executions that have not reached a terminal state. Retention must treat these
// as live references: deleting one would make the execution unrecoverable.
func (s *ExecutionStore) ReferencedArtifactHashes(ctx context.Context) (map[string]bool, error) {
	executions, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	hashes := make(map[string]bool)
	for _, execution := range executions {
		if execution.IsTerminal() || execution.Hash == "" {
			continue
		}
		hashes[execution.Hash] = true
	}
	return hashes, nil
}

// ListByInstance returns every execution belonging to instanceID from both
// memory and durable storage. Storage and decode failures are returned.
func (s *ExecutionStore) ListByInstance(ctx context.Context, instanceID shared.ID) ([]*Execution, error) {
	all, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]*Execution, 0)
	for _, exec := range all {
		if exec.InstanceID == instanceID {
			result = append(result, exec)
		}
	}
	return result, nil
}

func (s *ExecutionStore) loadFromStore(ctx context.Context, executionID shared.ID) (*Execution, error) {
	key, err := s.key(executionID)
	if err != nil {
		return nil, err
	}
	data, err := s.store.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	exec, err := UnmarshalExecution(data)
	if err != nil {
		return nil, fmt.Errorf("decode execution %s: %w", executionID, err)
	}
	if exec.ID != executionID {
		return nil, fmt.Errorf("decode execution %s: stored id is %s", executionID, exec.ID)
	}
	return exec, nil
}

// Reload returns a fresh copy of executionID read directly from durable storage,
// bypassing the live memory cache. An idempotent wait replay writes nothing, so
// the caller's in-memory Execution may already have been mutated; Reload is what
// lets it restore durable state before attempting another transition.
func (s *ExecutionStore) Reload(ctx context.Context, executionID shared.ID) (*Execution, error) {
	return s.loadFromStore(ctx, executionID)
}

// GetSignalDelivery returns the durable delivery record for deliveryID, or
// storage.ErrNotFound when it has not been recorded. The scheduler uses it to
// recognize an idempotent replay before mutating an execution for a delivery
// that was already accepted.
func (s *ExecutionStore) GetSignalDelivery(ctx context.Context, executionID shared.ID, deliveryID string) (*SignalDelivery, error) {
	key, err := SignalDeliveryKey(executionID, deliveryID)
	if err != nil {
		return nil, err
	}
	data, err := s.store.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	var delivery SignalDelivery
	if err := json.Unmarshal(data, &delivery); err != nil {
		return nil, fmt.Errorf("decode signal delivery %s: %w", key, err)
	}
	return &delivery, nil
}

// DueWaitTimers returns at most limit due, unleased wait timers ordered by due
// time then key, including timers whose due time passed while the process was
// down. It is the discovery half of the scheduler's due-timer poller; the timer
// substrate owns the due-time query so the poller never scans the timer space.
func (s *ExecutionStore) DueWaitTimers(ctx context.Context, now time.Time, limit int) ([]storage.Timer, error) {
	return s.store.DueTimers(ctx, now, limit)
}

// WaitTimerKeys returns the wait timer keys in one execution, optionally
// narrowed to a single capability when capabilityID is set. Prefix construction
// is delegated to storage so the timer key format has one authority
// (storage.TimerKey/TimerPrefix); the "timers" segment is that namespace. The
// read is bounded to the requested scope and never lists the whole timer space.
// It backs cancellation, which must remove every timer an execution owns, and
// the poller's safety check for a timer that conflicts with a future deadline.
func (s *ExecutionStore) WaitTimerKeys(ctx context.Context, executionID shared.ID, capabilityID shared.ID) ([]string, error) {
	var prefix string
	var err error
	if capabilityID == "" {
		prefix, err = storage.Prefix("timers", string(executionID))
	} else {
		prefix, err = storage.TimerPrefix(string(executionID), string(capabilityID))
	}
	if err != nil {
		return nil, err
	}
	return s.store.ListTimers(ctx, prefix)
}

func (s *ExecutionStore) key(id shared.ID) (string, error) {
	return storage.Key("executions", string(id))
}

func executionIDFromKey(key string) (shared.ID, error) {
	prefix, err := storage.Prefix("executions")
	if err != nil {
		return "", err
	}
	if len(key) <= len(prefix) || key[:len(prefix)] != prefix {
		return "", fmt.Errorf("decode execution key %q: unexpected prefix", key)
	}
	return shared.ID(key[len(prefix):]), nil
}

// storedExecution reads the persisted execution bytes and revision inside the
// caller's transaction. The bytes are the compare-and-swap precondition, so the
// revision check and the write cannot be separated by another writer. A missing
// record returns nil bytes and revision zero without an error; CompareAndSwap
// reports the absence when the caller attempts to write.
func storedExecution(ctx context.Context, tx storage.Tx, key string) ([]byte, uint64, error) {
	data, err := tx.Get(ctx, key)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("read persisted execution revision: %w", err)
	}
	var record struct {
		Revision uint64 `json:"revision"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, 0, fmt.Errorf("decode persisted execution revision: %w", err)
	}
	return data, record.Revision, nil
}

func marshalExecutionAt(execution *Execution, revision uint64) ([]byte, error) {
	return json.Marshal(execution.snapshotAt(revision))
}

// writeExecutionRevision compares execution's revision with the persisted one
// and writes its next snapshot inside tx. It is the single implementation of
// the compare-and-swap precondition shared by every transition path.
func writeExecutionRevision(ctx context.Context, tx storage.KV, key string, execution *Execution) (uint64, error) {
	old, currentRevision, err := storedExecution(ctx, tx, key)
	if err != nil {
		return 0, err
	}
	if execution.Revision() != currentRevision {
		return 0, fmt.Errorf("%w: execution %s is at revision %d, stored revision is %d",
			storage.ErrConflict, execution.ID, execution.Revision(), currentRevision)
	}
	nextRevision := currentRevision + 1
	data, err := marshalExecutionAt(execution, nextRevision)
	if err != nil {
		return 0, err
	}
	if err := tx.CompareAndSwap(ctx, key, old, data); err != nil {
		return 0, err
	}
	return nextRevision, nil
}

// pendingDispatchIntent holds an intent's resolved key and a copy of its
// record. Copies keep a rolled-back transaction from mutating caller-owned
// intents.
type pendingDispatchIntent struct {
	key          string
	record       DispatchIntent
	setCreatedAt bool
}

func prepareDispatchIntents(intents []*DispatchIntent) ([]pendingDispatchIntent, error) {
	pending := make([]pendingDispatchIntent, 0, len(intents))
	for _, intent := range intents {
		if intent == nil {
			return nil, fmt.Errorf("dispatch intent is required")
		}
		if intent.ExecutionID == "" || intent.CapabilityID == "" || intent.AttemptID == "" {
			return nil, fmt.Errorf("dispatch intent requires execution, capability, and attempt IDs")
		}
		intentKey, err := dispatchIntentKey(intent.ExecutionID, intent.CapabilityID, intent.AttemptID)
		if err != nil {
			return nil, err
		}
		pending = append(pending, pendingDispatchIntent{
			key:          intentKey,
			record:       *intent,
			setCreatedAt: intent.CreatedAt.IsZero(),
		})
	}
	return pending, nil
}

func writeDispatchIntents(ctx context.Context, tx storage.KV, pending []pendingDispatchIntent, createdAt time.Time) error {
	for i := range pending {
		item := &pending[i]
		if item.setCreatedAt {
			item.record.CreatedAt = createdAt
		}
		encoded, err := json.Marshal(&item.record)
		if err != nil {
			return fmt.Errorf("marshal dispatch intent %s: %w", item.key, err)
		}
		if err := tx.Put(ctx, item.key, encoded); err != nil {
			return fmt.Errorf("write dispatch intent %s: %w", item.key, err)
		}
	}
	return nil
}

func applyDispatchIntentTimestamps(intents []*DispatchIntent, pending []pendingDispatchIntent) {
	for i := range pending {
		if pending[i].setCreatedAt {
			intents[i].CreatedAt = pending[i].record.CreatedAt
		}
	}
}

// DispatchIntent represents a unit of ready work that a worker should claim and
// dispatch. It is persisted so that a restart does not lose track of ready
// capabilities. Owner identifies the current claim; LeaseExpiresAt bounds it so
// a crashed owner's work becomes claimable again.
type DispatchIntent struct {
	ExecutionID    shared.ID  `json:"execution_id"`
	CapabilityID   shared.ID  `json:"capability_id"`
	ReadyToRun     bool       `json:"ready_to_run"`
	AttemptID      shared.ID  `json:"attempt_id"`
	CreatedAt      time.Time  `json:"created_at"`
	Owner          string     `json:"owner,omitempty"`
	LeaseExpiresAt *time.Time `json:"lease_expires_at,omitempty"`
}

func dispatchIntentKey(executionID, capabilityID, attemptID shared.ID) (string, error) {
	return storage.Key("dispatch_intents", string(executionID), string(capabilityID), string(attemptID))
}

// NewDispatchIntent builds the ready dispatch intent for one capability attempt.
// It keeps intent construction in one place so callers do not duplicate the
// identity fields that the store indexes intents by: the (execution, capability,
// attempt) triple is the intent's stable identity, so replaying readiness maps
// to the same intent rather than creating a second one.
func NewDispatchIntent(executionID, capabilityID, attemptID shared.ID) *DispatchIntent {
	return &DispatchIntent{
		ExecutionID:  executionID,
		CapabilityID: capabilityID,
		AttemptID:    attemptID,
		ReadyToRun:   true,
	}
}

// EnqueueDispatchIntent records a ready dispatch intent. It is idempotent: an
// intent already stored under the same execution/capability/attempt key keeps
// its original creation time and any existing claim, so replaying readiness
// cannot steal work that a worker is already running.
func (s *ExecutionStore) EnqueueDispatchIntent(ctx context.Context, intent *DispatchIntent) error {
	if intent == nil {
		return fmt.Errorf("dispatch intent is required")
	}
	key, err := dispatchIntentKey(intent.ExecutionID, intent.CapabilityID, intent.AttemptID)
	if err != nil {
		return err
	}

	setCreatedAt := intent.CreatedAt.IsZero()
	var persistedCreatedAt time.Time
	err = s.store.WithTx(ctx, func(tx storage.Tx) error {
		record := *intent
		data, err := tx.Get(ctx, key)
		switch {
		case err == nil:
			var existing DispatchIntent
			if err := json.Unmarshal(data, &existing); err != nil {
				return fmt.Errorf("decode dispatch intent %s: %w", key, err)
			}
			if !existing.CreatedAt.IsZero() {
				record.CreatedAt = existing.CreatedAt
			}
			if existing.Owner != "" {
				record.Owner = existing.Owner
				record.LeaseExpiresAt = existing.LeaseExpiresAt
			}
		case !errors.Is(err, storage.ErrNotFound):
			return fmt.Errorf("read dispatch intent %s: %w", key, err)
		}

		if record.CreatedAt.IsZero() {
			record.CreatedAt = time.Now().UTC()
		}
		persistedCreatedAt = record.CreatedAt
		encoded, err := json.Marshal(&record)
		if err != nil {
			return fmt.Errorf("marshal dispatch intent %s: %w", key, err)
		}
		return tx.Put(ctx, key, encoded)
	})
	if err != nil {
		return fmt.Errorf("persist dispatch intent %s: %w", key, err)
	}
	if setCreatedAt {
		intent.CreatedAt = persistedCreatedAt
	}
	return nil
}

// ClaimDispatchIntent attempts to take ownership of a dispatch intent. A claim
// succeeds only while the intent is ready and is either unowned or has an
// expired lease. The read and eligibility check happen in one transaction, and
// the write is a compare-and-swap against the value that was read, so
// concurrent callers cannot both observe the intent as claimable and both win.
// It returns (nil, false, nil) when the intent is missing, unready, held under a
// live lease, or lost the compare-and-swap to another owner.
func (s *ExecutionStore) ClaimDispatchIntent(ctx context.Context, intent *DispatchIntent, owner string, lease time.Duration) (*DispatchIntent, bool, error) {
	if intent == nil {
		return nil, false, fmt.Errorf("dispatch intent is required")
	}
	if owner == "" {
		return nil, false, fmt.Errorf("dispatch intent owner is required")
	}
	if lease <= 0 {
		return nil, false, fmt.Errorf("dispatch intent lease must be positive")
	}
	key, err := dispatchIntentKey(intent.ExecutionID, intent.CapabilityID, intent.AttemptID)
	if err != nil {
		return nil, false, err
	}
	return s.claimDispatchIntent(ctx, key, owner, lease)
}

func (s *ExecutionStore) claimDispatchIntent(ctx context.Context, key, owner string, lease time.Duration) (*DispatchIntent, bool, error) {
	var claimed *DispatchIntent
	err := s.store.WithTx(ctx, func(tx storage.Tx) error {
		data, err := tx.Get(ctx, key)
		if errors.Is(err, storage.ErrNotFound) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read dispatch intent %s: %w", key, err)
		}

		var intent DispatchIntent
		if err := json.Unmarshal(data, &intent); err != nil {
			return fmt.Errorf("decode dispatch intent %s: %w", key, err)
		}
		now := time.Now().UTC()
		if !dispatchIntentClaimable(&intent, now) {
			return nil
		}

		expires := now.Add(lease)
		intent.Owner = owner
		intent.LeaseExpiresAt = &expires
		encoded, err := json.Marshal(&intent)
		if err != nil {
			return fmt.Errorf("marshal dispatch intent %s: %w", key, err)
		}
		// The eligibility check above used the value read in this transaction.
		// CompareAndSwap makes the claim atomic: if another owner wrote after
		// that read, the conditional update loses and this caller reports no
		// claim instead of overwriting the winner.
		if err := tx.CompareAndSwap(ctx, key, data, encoded); err != nil {
			if errors.Is(err, storage.ErrConflict) || errors.Is(err, storage.ErrNotFound) {
				return nil
			}
			return fmt.Errorf("write dispatch intent %s: %w", key, err)
		}
		claimed = &intent
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return claimed, claimed != nil, nil
}

// dispatchIntentClaimable reports whether a stored intent may be claimed at
// now. An intent held by an owner without a lease expiry is treated as live so
// a malformed claim cannot cause duplicate dispatch.
func dispatchIntentClaimable(intent *DispatchIntent, now time.Time) bool {
	if !intent.ReadyToRun {
		return false
	}
	if intent.Owner == "" {
		return true
	}
	return intent.LeaseExpiresAt != nil && !intent.LeaseExpiresAt.After(now)
}

// ReleaseDispatchIntent clears the claim only when owner still holds it. It is a
// no-op for a missing intent or a different owner, so a stale worker cannot
// release work that has since been re-claimed.
func (s *ExecutionStore) ReleaseDispatchIntent(ctx context.Context, executionID, capabilityID, attemptID shared.ID, owner string) error {
	if owner == "" {
		return fmt.Errorf("dispatch intent owner is required")
	}
	key, err := dispatchIntentKey(executionID, capabilityID, attemptID)
	if err != nil {
		return err
	}

	err = s.store.WithTx(ctx, func(tx storage.Tx) error {
		data, err := tx.Get(ctx, key)
		if errors.Is(err, storage.ErrNotFound) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read dispatch intent %s: %w", key, err)
		}

		var intent DispatchIntent
		if err := json.Unmarshal(data, &intent); err != nil {
			return fmt.Errorf("decode dispatch intent %s: %w", key, err)
		}
		if intent.Owner != owner {
			return nil
		}

		intent.Owner = ""
		intent.LeaseExpiresAt = nil
		encoded, err := json.Marshal(&intent)
		if err != nil {
			return fmt.Errorf("marshal dispatch intent %s: %w", key, err)
		}
		return tx.Put(ctx, key, encoded)
	})
	if err != nil {
		return fmt.Errorf("release dispatch intent %s: %w", key, err)
	}
	return nil
}

// CompleteDispatchIntent deletes the intent only when owner still holds it. It
// is idempotent: a missing intent or a different owner is a no-op, so a late
// completion cannot delete a newer claim.
func (s *ExecutionStore) CompleteDispatchIntent(ctx context.Context, executionID, capabilityID, attemptID shared.ID, owner string) error {
	if owner == "" {
		return fmt.Errorf("dispatch intent owner is required")
	}
	key, err := dispatchIntentKey(executionID, capabilityID, attemptID)
	if err != nil {
		return err
	}

	err = s.store.WithTx(ctx, func(tx storage.Tx) error {
		data, err := tx.Get(ctx, key)
		if errors.Is(err, storage.ErrNotFound) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read dispatch intent %s: %w", key, err)
		}

		var intent DispatchIntent
		if err := json.Unmarshal(data, &intent); err != nil {
			return fmt.Errorf("decode dispatch intent %s: %w", key, err)
		}
		if intent.Owner != owner {
			return nil
		}
		return tx.Delete(ctx, key)
	})
	if err != nil {
		return fmt.Errorf("complete dispatch intent %s: %w", key, err)
	}
	return nil
}

// ClaimReadyDispatchIntents claims every ready, claimable intent for owner and
// returns only the intents this call won. It is the startup reconciliation
// primitive: leases that outlived a crashed owner become claimable once they
// expire, while intents under a live lease are left untouched. Intents are
// claimed individually, so a concurrent reconciler may win some of them.
func (s *ExecutionStore) ClaimReadyDispatchIntents(ctx context.Context, owner string, lease time.Duration) ([]*DispatchIntent, error) {
	if owner == "" {
		return nil, fmt.Errorf("dispatch intent owner is required")
	}
	if lease <= 0 {
		return nil, fmt.Errorf("dispatch intent lease must be positive")
	}
	prefix, err := storage.Prefix("dispatch_intents")
	if err != nil {
		return nil, fmt.Errorf("prefix dispatch intents: %w", err)
	}
	keys, err := s.store.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("list dispatch intents: %w", err)
	}

	claimed := make([]*DispatchIntent, 0, len(keys))
	for _, key := range keys {
		intent, won, err := s.claimDispatchIntent(ctx, key, owner, lease)
		if err != nil {
			return claimed, fmt.Errorf("claim dispatch intents: %w", err)
		}
		if won {
			claimed = append(claimed, intent)
		}
	}
	return claimed, nil
}

// SignalDeliveryStatus is the outcome returned to the original sender and
// retained with the delivery record.
type SignalDeliveryStatus string

const (
	// SignalDeliveryAccepted means the delivery matched a waiting capability
	// and was committed with its resolution.
	SignalDeliveryAccepted SignalDeliveryStatus = "accepted"
)

// SignalDelivery is the durable record of one accepted signal delivery. It is
// stored at SignalDeliveryKey and exists only for matched attempts; an
// unmatched delivery is not recorded so a later retry can still succeed.
// PayloadHash binds an idempotent replay to the original payload without
// retaining the payload itself.
type SignalDelivery struct {
	DeliveryID     string               `json:"delivery_id"`
	ExecutionID    shared.ID            `json:"execution_id"`
	SignalName     string               `json:"signal_name"`
	CorrelationKey string               `json:"correlation_key,omitempty"`
	CapabilityID   shared.ID            `json:"capability_id"`
	PayloadHash    string               `json:"payload_hash"`
	Status         SignalDeliveryStatus `json:"status"`
	CreatedAt      time.Time            `json:"created_at"`
}

// NewSignalDelivery builds a delivery record and hashes its payload for
// duplicate detection. Validation happens here so the persistence boundary
// never has to guess what identifies a delivery.
func NewSignalDelivery(executionID, capabilityID shared.ID, deliveryID, signalName, correlationKey string, payload map[string]any) (*SignalDelivery, error) {
	if executionID == "" || capabilityID == "" {
		return nil, fmt.Errorf("signal delivery requires execution and capability IDs")
	}
	if deliveryID == "" {
		return nil, fmt.Errorf("signal delivery id is required")
	}
	if signalName == "" {
		return nil, fmt.Errorf("signal name is required")
	}
	encoded, err := json.Marshal(data.CanonicalMap(payload))
	if err != nil {
		return nil, fmt.Errorf("marshal signal payload: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return &SignalDelivery{
		DeliveryID:     deliveryID,
		ExecutionID:    executionID,
		SignalName:     signalName,
		CorrelationKey: correlationKey,
		CapabilityID:   capabilityID,
		PayloadHash:    hex.EncodeToString(sum[:]),
		Status:         SignalDeliveryAccepted,
	}, nil
}

// SignalDeliveryKey returns the durable key for a delivery record. Key
// validation rejects traversal segments in deliveryID.
func SignalDeliveryKey(executionID shared.ID, deliveryID string) (string, error) {
	if deliveryID == "" {
		return "", fmt.Errorf("signal delivery id is required")
	}
	return storage.Key("signals", string(executionID), deliveryID)
}

// WaitTimerKey returns the durable timer key for one wait or retry attempt. It
// delegates to the storage key constructor so the execution layer does not
// duplicate the timer key format. A resumed capability gets a new attempt ID,
// so its next wait writes a new timer rather than reusing a stale one.
func WaitTimerKey(executionID, capabilityID, attemptID shared.ID) (string, error) {
	if executionID == "" || capabilityID == "" || attemptID == "" {
		return "", fmt.Errorf("wait timer requires execution, capability, and attempt IDs")
	}
	return storage.TimerKey(string(executionID), string(capabilityID), string(attemptID))
}

// WaitTimerPayload is the opaque payload stored with a wait timer. The
// execution snapshot remains authoritative; this record only identifies which
// capability a due timer should wake.
type WaitTimerPayload struct {
	ExecutionID  shared.ID `json:"execution_id"`
	CapabilityID shared.ID `json:"capability_id"`
	WaitType     WaitType  `json:"wait_type,omitempty"`
	AttemptID    shared.ID `json:"attempt_id,omitempty"`
}

// EncodeWaitTimer serializes a wait timer payload. There is one encoding so
// every writer and the due-timer consumer agree on the record shape.
func EncodeWaitTimer(payload WaitTimerPayload) ([]byte, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal wait timer payload: %w", err)
	}
	return encoded, nil
}

// DecodeWaitTimer parses a wait timer payload. Invalid data is an error, never
// an empty payload, so a corrupt index entry cannot silently wake work.
func DecodeWaitTimer(data []byte) (*WaitTimerPayload, error) {
	var payload WaitTimerPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("decode wait timer payload: %w", err)
	}
	if payload.ExecutionID == "" || payload.CapabilityID == "" {
		return nil, fmt.Errorf("wait timer payload requires execution and capability IDs")
	}
	return &payload, nil
}

// WaitTimerWrite is one timer index entry to upsert as part of a transition.
type WaitTimerWrite struct {
	Key   string
	DueAt time.Time
	Data  []byte
}

// WaitTimerDelete removes a timer index entry as part of a transition. Owner,
// when set, is the fencing token from a claim and makes the delete conditional
// on still holding that claim. RequireExisting fails the transition when the
// timer is already gone; an unfenced, non-required delete is best-effort.
type WaitTimerDelete struct {
	Key             string
	Owner           string
	RequireExisting bool
}

// WaitEffects are the derived records that must become durable with one wait
// transition. Each field is optional so entry, resolution, cancellation,
// retry, and deadline transitions can share one commit method.
type WaitEffects struct {
	PutTimers    []WaitTimerWrite
	DeleteTimers []WaitTimerDelete
	Delivery     *SignalDelivery
	Intents      []*DispatchIntent
}

// WaitTransitionRequest is one atomic wait transition. Execution must already
// carry the new state; the store persists its next revision together with
// Effects in a single WithTimer transaction.
type WaitTransitionRequest struct {
	Execution *Execution
	Effects   WaitEffects
}

// WaitTransitionResult reports what a wait transition committed.
type WaitTransitionResult struct {
	// Revision is the new snapshot revision. It is zero on an idempotent replay.
	Revision uint64
	// Replay is true when the delivery ID was already recorded with the same
	// payload. No writes occurred in that case, and Execution's revision is
	// unchanged. The caller's in-memory Execution may already have been mutated
	// before the call, so it must be reloaded before another transition.
	Replay bool
	// Delivery is the record written by this transition or found on replay.
	Delivery *SignalDelivery
}

// SaveWaitTransition atomically commits a wait state change with the timer,
// delivery, and dispatch intent records it produced. Nothing is published
// here: callers publish only after a successful return, and the durable intent
// is the source of truth if publishing fails.
//
// A repeated delivery ID with the same payload is an idempotent replay: no
// snapshot, timer, or intent is written. A different payload returns
// ErrSignalDeliveryConflict. A fenced timer delete that has lost its lease
// returns ErrWaitTimerLeaseLost, and a required missing timer returns
// ErrWaitTimerMissing; both leave all durable and in-memory state untouched.
func (s *ExecutionStore) SaveWaitTransition(ctx context.Context, req WaitTransitionRequest) (*WaitTransitionResult, error) {
	execution := req.Execution
	if execution == nil {
		return nil, fmt.Errorf("execution is required")
	}
	execution.saveMu.Lock()
	defer execution.saveMu.Unlock()

	key, err := s.key(execution.ID)
	if err != nil {
		return nil, err
	}
	pending, err := prepareDispatchIntents(req.Effects.Intents)
	if err != nil {
		return nil, err
	}

	delivery := req.Effects.Delivery
	deliveryKey := ""
	if delivery != nil {
		if delivery.DeliveryID == "" || delivery.ExecutionID == "" || delivery.CapabilityID == "" || delivery.PayloadHash == "" {
			return nil, fmt.Errorf("signal delivery requires id, execution, capability, and payload hash")
		}
		if delivery.ExecutionID != execution.ID {
			return nil, fmt.Errorf("signal delivery execution %s does not match transition execution %s", delivery.ExecutionID, execution.ID)
		}
		deliveryKey, err = SignalDeliveryKey(delivery.ExecutionID, delivery.DeliveryID)
		if err != nil {
			return nil, err
		}
	}
	for _, write := range req.Effects.PutTimers {
		if write.Key == "" || write.DueAt.IsZero() {
			return nil, fmt.Errorf("wait timer write requires a key and a due time")
		}
	}
	for _, del := range req.Effects.DeleteTimers {
		if del.Key == "" {
			return nil, fmt.Errorf("wait timer delete requires a key")
		}
	}

	var nextRevision uint64
	result := &WaitTransitionResult{}
	createdAt := time.Now().UTC()
	err = s.store.WithTimer(ctx, func(tx storage.TimerTx) error {
		if delivery != nil {
			data, err := tx.Get(ctx, deliveryKey)
			switch {
			case err == nil:
				var existing SignalDelivery
				if err := json.Unmarshal(data, &existing); err != nil {
					return fmt.Errorf("decode signal delivery %s: %w", deliveryKey, err)
				}
				if existing.PayloadHash != delivery.PayloadHash {
					return ErrSignalDeliveryConflict
				}
				result.Replay = true
				result.Delivery = &existing
				return nil
			case !errors.Is(err, storage.ErrNotFound):
				return fmt.Errorf("read signal delivery %s: %w", deliveryKey, err)
			}
		}

		for _, del := range req.Effects.DeleteTimers {
			if del.Owner != "" {
				deleted, err := tx.DeleteTimerOwned(ctx, del.Key, del.Owner)
				if err != nil {
					return fmt.Errorf("delete owned wait timer %s: %w", del.Key, err)
				}
				if !deleted {
					return ErrWaitTimerLeaseLost
				}
				continue
			}
			_, err := tx.GetTimer(ctx, del.Key)
			if errors.Is(err, storage.ErrTimerNotFound) {
				if del.RequireExisting {
					return ErrWaitTimerMissing
				}
				continue
			}
			if err != nil {
				return fmt.Errorf("read wait timer %s: %w", del.Key, err)
			}
			if err := tx.DeleteTimer(ctx, del.Key); err != nil {
				return fmt.Errorf("delete wait timer %s: %w", del.Key, err)
			}
		}

		next, err := writeExecutionRevision(ctx, tx, key, execution)
		if err != nil {
			return err
		}
		nextRevision = next

		for _, write := range req.Effects.PutTimers {
			if err := tx.PutTimer(ctx, write.Key, write.DueAt, write.Data); err != nil {
				return fmt.Errorf("write wait timer %s: %w", write.Key, err)
			}
		}
		if delivery != nil {
			record := *delivery
			if record.CreatedAt.IsZero() {
				record.CreatedAt = createdAt
			}
			encoded, err := json.Marshal(&record)
			if err != nil {
				return fmt.Errorf("marshal signal delivery %s: %w", deliveryKey, err)
			}
			if err := tx.Put(ctx, deliveryKey, encoded); err != nil {
				return fmt.Errorf("write signal delivery %s: %w", deliveryKey, err)
			}
			result.Delivery = &record
		}
		return writeDispatchIntents(ctx, tx, pending, createdAt)
	})
	if err != nil {
		return nil, fmt.Errorf("persist wait transition %s: %w", execution.ID, err)
	}
	if result.Replay {
		return result, nil
	}

	execution.setRevision(nextRevision)
	applyDispatchIntentTimestamps(req.Effects.Intents, pending)
	if err := s.mem.Save(ctx, execution); err != nil {
		return nil, fmt.Errorf("record execution %s in memory after durable wait transition: %w", execution.ID, err)
	}
	result.Revision = nextRevision
	return result, nil
}

// ClaimWaitTimer claims a due timer under a fresh owner token. Owner must be a
// non-empty, opaque per-claim value; reusing one across claims breaks fencing.
// It returns false when the timer is missing, not yet due, or under a live
// lease.
func (s *ExecutionStore) ClaimWaitTimer(ctx context.Context, key, owner string, lease time.Duration) (bool, error) {
	if key == "" {
		return false, fmt.Errorf("wait timer key is required")
	}
	if owner == "" {
		return false, storage.ErrTimerOwnerEmpty
	}
	if lease <= 0 {
		return false, fmt.Errorf("wait timer lease must be positive")
	}
	return s.store.ClaimTimer(ctx, key, owner, lease)
}

// RenewWaitTimer extends a live lease held by owner. Lease loss is reported as
// ErrWaitTimerLeaseLost instead of an ambiguous false so callers stop work
// before attempting a fenced commit.
func (s *ExecutionStore) RenewWaitTimer(ctx context.Context, key, owner string, lease time.Duration) error {
	if key == "" {
		return fmt.Errorf("wait timer key is required")
	}
	if owner == "" {
		return storage.ErrTimerOwnerEmpty
	}
	if lease <= 0 {
		return fmt.Errorf("wait timer lease must be positive")
	}
	ok, err := s.store.RenewTimer(ctx, key, owner, lease)
	if err != nil {
		return err
	}
	if !ok {
		return ErrWaitTimerLeaseLost
	}
	return nil
}

// DeleteWaitTimerOwned removes a timer only while owner still holds the claim.
// Lease loss is reported explicitly; a timer already removed by the owner's
// own committed transition returns false because it is no longer owned.
func (s *ExecutionStore) DeleteWaitTimerOwned(ctx context.Context, key, owner string) (bool, error) {
	if key == "" {
		return false, fmt.Errorf("wait timer key is required")
	}
	if owner == "" {
		return false, storage.ErrTimerOwnerEmpty
	}
	return s.store.DeleteTimerOwned(ctx, key, owner)
}
