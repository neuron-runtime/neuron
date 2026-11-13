package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/storage"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

// ExecutionStore is the durable execution repository. Memory is a read-through
// cache for live executions; durable state is authoritative for everything not
// currently live.
//
// Concurrency contract: Save serializes writes for one Execution value with a
// per-execution mutex, then compares the snapshot revision with the stored
// revision inside one transaction. Two writers sharing the same Execution value
// therefore cannot conflict with one another; a conflict means another store or
// process holds a stale copy. That distinction is why the revision guard is
// meaningful instead of merely rejecting ordinary same-process races.
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
		old, currentRevision, err := storedExecution(ctx, tx, key)
		if err != nil {
			return err
		}
		if execution.Revision() != currentRevision {
			return fmt.Errorf("%w: execution %s is at revision %d, stored revision is %d",
				storage.ErrConflict, execution.ID, execution.Revision(), currentRevision)
		}

		nextRevision = currentRevision + 1
		data, err := marshalExecutionAt(execution, nextRevision)
		if err != nil {
			return err
		}
		return tx.CompareAndSwap(ctx, key, old, data)
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
	type pendingIntent struct {
		key          string
		record       DispatchIntent
		setCreatedAt bool
	}
	pending := make([]pendingIntent, 0, len(intents))
	for _, intent := range intents {
		if intent == nil {
			return fmt.Errorf("dispatch intent is required")
		}
		if intent.ExecutionID == "" || intent.CapabilityID == "" || intent.AttemptID == "" {
			return fmt.Errorf("dispatch intent requires execution, capability, and attempt IDs")
		}
		intentKey, err := dispatchIntentKey(intent.ExecutionID, intent.CapabilityID, intent.AttemptID)
		if err != nil {
			return err
		}
		pending = append(pending, pendingIntent{
			key:          intentKey,
			record:       *intent,
			setCreatedAt: intent.CreatedAt.IsZero(),
		})
	}

	var nextRevision uint64
	createdAt := time.Now().UTC()
	err = s.store.WithTx(ctx, func(tx storage.Tx) error {
		old, currentRevision, err := storedExecution(ctx, tx, key)
		if err != nil {
			return err
		}
		if execution.Revision() != currentRevision {
			return fmt.Errorf("%w: execution %s is at revision %d, stored revision is %d",
				storage.ErrConflict, execution.ID, execution.Revision(), currentRevision)
		}

		nextRevision = currentRevision + 1
		data, err := marshalExecutionAt(execution, nextRevision)
		if err != nil {
			return err
		}
		if err := tx.CompareAndSwap(ctx, key, old, data); err != nil {
			return err
		}

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
	})
	if err != nil {
		return fmt.Errorf("persist execution %s transition: %w", execution.ID, err)
	}

	execution.setRevision(nextRevision)
	for i := range pending {
		if pending[i].setCreatedAt {
			intents[i].CreatedAt = pending[i].record.CreatedAt
		}
	}
	if err := s.mem.Save(ctx, execution); err != nil {
		return fmt.Errorf("record execution %s in memory after durable transition: %w", execution.ID, err)
	}
	return nil
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
