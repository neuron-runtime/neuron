package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

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
	indexKey, hasIndex, err := executionDeadlineIndexKey(execution)
	if err != nil {
		return err
	}

	err = s.store.WithTx(ctx, func(tx storage.Tx) error {
		if _, err := tx.Get(ctx, key); err == nil {
			return fmt.Errorf("%w: execution %s", storage.ErrAlreadyExists, execution.ID)
		} else if !errors.Is(err, storage.ErrNotFound) {
			return fmt.Errorf("read execution %s: %w", execution.ID, err)
		}
		if err := tx.Put(ctx, key, data); err != nil {
			return err
		}
		if hasIndex {
			if err := tx.Put(ctx, indexKey, []byte(execution.ID)); err != nil {
				return fmt.Errorf("write execution deadline index %s: %w", indexKey, err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("persist execution %s: %w", execution.ID, err)
	}

	if err := s.mem.Add(ctx, execution); err != nil {
		// Memory refused the execution after the durable write succeeded. Remove
		// the durable record and index so a later restart does not surface an
		// execution the live store never accepted.
		cleanupErr := s.store.Delete(ctx, key)
		if hasIndex {
			cleanupErr = errors.Join(cleanupErr, s.store.Delete(ctx, indexKey))
		}
		if cleanupErr != nil {
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
		if err := writeDispatchIntents(ctx, tx, pending, createdAt); err != nil {
			return err
		}
		if execution.IsTerminal() {
			return clearDispatchReadyIndexInTx(ctx, tx, execution.ID)
		}
		return nil
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

// Adopt records an execution as live without writing its snapshot back to
// durable storage. Recovery uses it after loading a snapshot and attaching its
// recompiled blueprint: the scheduler and engine must share one Execution value,
// and a fresh Get would otherwise load a second copy from storage. The deadline
// index is reconciled atomically so a recovered deadline becomes discoverable
// without leaving entries from earlier recoveries behind.
//
// Adopt deliberately does not change the revision. The snapshot it is given is
// the durable state the caller just read, so the next transition compares
// against that revision exactly as it would after a normal load.
func (s *ExecutionStore) Adopt(ctx context.Context, execution *Execution) error {
	if execution == nil {
		return fmt.Errorf("execution is required")
	}
	key, err := s.key(execution.ID)
	if err != nil {
		return err
	}
	// Recovery is the backfill point for snapshots written before the deadline
	// index existed. The durable snapshot cannot be the only source of the prior
	// key: recovery may stamp a deadline that the snapshot never recorded, so a
	// second restart would otherwise write another index entry beside the first.
	// The execution ID carried by every index entry is the stable identity used
	// to reconcile all of this execution's entries in one transaction.
	var oldData []byte
	err = s.store.WithTx(ctx, func(tx storage.Tx) error {
		data, err := tx.Get(ctx, key)
		if err == nil {
			oldData = data
		} else if !errors.Is(err, storage.ErrNotFound) {
			return fmt.Errorf("read execution %s for deadline index: %w", execution.ID, err)
		}
		return reconcileAdoptedDeadlineIndexInTx(ctx, tx, oldData, execution)
	})
	if err != nil {
		return fmt.Errorf("adopt execution %s: %w", execution.ID, err)
	}
	if err := s.mem.Save(ctx, execution); err != nil {
		return fmt.Errorf("adopt execution %s: %w", execution.ID, err)
	}
	return nil
}

// ClearDispatchIntents removes every dispatch intent belonging to execution,
// together with its ready-index entries.
//
// Recovery calls it before re-deriving intents from the recovered state. A
// crashed process can leave an intent claimed under a lease that has not yet
// expired, or an intent for a capability that has since completed; neither is
// valid work, and the lease would otherwise delay or duplicate dispatch.
func (s *ExecutionStore) ClearDispatchIntents(ctx context.Context, executionID shared.ID) error {
	return s.store.WithTx(ctx, func(tx storage.Tx) error {
		return clearDispatchIntentsInTx(ctx, tx, executionID)
	})
}

// clearDispatchIntentsInTx removes an execution's intent records and their ready
// index in one transaction. Recovery calls it before re-deriving intents.
func clearDispatchIntentsInTx(ctx context.Context, tx storage.KV, executionID shared.ID) error {
	intentPrefix, err := storage.Prefix(dispatchIntentNamespace, string(executionID))
	if err != nil {
		return fmt.Errorf("prefix dispatch intents for execution %s: %w", executionID, err)
	}
	intentKeys, err := tx.List(ctx, intentPrefix)
	if err != nil {
		return fmt.Errorf("list dispatch intents for execution %s: %w", executionID, err)
	}
	for _, key := range intentKeys {
		if err := tx.Delete(ctx, key); err != nil {
			return fmt.Errorf("delete dispatch intent %s: %w", key, err)
		}
	}
	return clearDispatchReadyIndexInTx(ctx, tx, executionID)
}

// clearDispatchReadyIndexInTx removes an execution's ready-index entries while
// leaving intent records intact. Terminal transitions use it so finished work is
// no longer discoverable by a batch claim, without invalidating an owner-fenced
// single-intent operation that raced with the terminal commit.
func clearDispatchReadyIndexInTx(ctx context.Context, tx storage.KV, executionID shared.ID) error {
	readyPrefix, err := storage.Prefix(dispatchReadyNamespace, string(executionID))
	if err != nil {
		return fmt.Errorf("prefix ready dispatch intents for execution %s: %w", executionID, err)
	}
	readyKeys, err := tx.List(ctx, readyPrefix)
	if err != nil {
		return fmt.Errorf("list ready dispatch intents for execution %s: %w", executionID, err)
	}
	for _, key := range readyKeys {
		if err := tx.Delete(ctx, key); err != nil {
			return fmt.Errorf("delete ready dispatch intent %s: %w", key, err)
		}
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
	// Adopt can index a deadline that exists only in memory, so the persisted
	// snapshot alone cannot name every entry this execution may own. Derive the
	// live key before opening the transaction; the accessor is mutex-protected.
	var liveKey string
	live, err := s.mem.Get(ctx, executionID)
	switch {
	case err == nil:
		liveKey, _, err = executionDeadlineIndexKeyIgnoringTerminal(live)
		if err != nil {
			return err
		}
	case errors.Is(err, storage.ErrNotFound):
		// Only a durable record exists; its snapshot supplies the key.
	default:
		return fmt.Errorf("read execution %s from memory: %w", executionID, err)
	}

	err = s.store.WithTx(ctx, func(tx storage.Tx) error {
		data, err := tx.Get(ctx, key)
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
			return err
		}
		// Delete is still issued for a missing durable record: callers rely on
		// backend failures surfacing even when only a live execution exists.
		if err := tx.Delete(ctx, key); err != nil {
			return err
		}
		return removeExecutionDeadlineIndexInTx(ctx, tx, data, liveKey)
	})
	if err != nil {
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

// DueExecutionDeadlines returns at most limit execution IDs owned by instanceID
// whose maximum-lifetime deadline is due at or before now. It reads only the
// dedicated time-ordered deadline index; it never lists execution records and
// never mutates the executions it reports. Callers re-read each execution before
// acting, so a stale entry left by a crashed writer is harmless.
func (s *ExecutionStore) DueExecutionDeadlines(ctx context.Context, instanceID shared.ID, now time.Time, limit int) ([]shared.ID, error) {
	if instanceID == "" {
		return nil, fmt.Errorf("instance ID is required")
	}
	if limit <= 0 {
		return []shared.ID{}, nil
	}
	prefix, err := storage.ExecutionDeadlinePrefix(string(instanceID))
	if err != nil {
		return nil, err
	}
	keys, err := s.store.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("list execution deadlines for instance %s: %w", instanceID, err)
	}
	result := make([]shared.ID, 0, limit)
	seen := make(map[shared.ID]bool, limit)
	for _, key := range keys {
		_, dueAt, rawID, err := storage.ParseExecutionDeadlineKey(key)
		if err != nil {
			return nil, err
		}
		// Keys are ordered by UTC due time, so the first future entry ends the
		// scan for this instance.
		if dueAt.After(now) {
			break
		}
		id := shared.ID(rawID)
		if seen[id] {
			continue
		}
		result = append(result, id)
		seen[id] = true
		if len(result) >= limit {
			break
		}
	}
	return result, nil
}

// DueWaitTimers returns at most limit due, unleased wait timers ordered by due
// time then key, including timers whose due time passed while the process was
// down. It is the discovery half of the scheduler's due-timer poller; the timer
// substrate owns the due-time query so the poller never scans the timer space.
func (s *ExecutionStore) DueWaitTimers(ctx context.Context, now time.Time, limit int) ([]storage.Timer, error) {
	return s.store.DueTimers(ctx, now, limit)
}

// DueWaitTimersForInstance returns at most limit due wait timers belonging to
// instanceID, ordered by due time then key. It is additive: the global
// DueWaitTimers contract remains unchanged while callers migrate to
// instance-scoped discovery. The index keys are hex ordered rather than time
// ordered, so this method reads every owned entry, filters by due time, and
// stops after limit matches. It only reads: claim and lease state are left to
// ClaimWaitTimer, and a timer under a live lease may still be returned.
func (s *ExecutionStore) DueWaitTimersForInstance(ctx context.Context, instanceID shared.ID, now time.Time, limit int) ([]storage.Timer, error) {
	if instanceID == "" {
		return nil, fmt.Errorf("instance ID is required")
	}
	if limit <= 0 {
		return []storage.Timer{}, nil
	}
	prefix, err := storage.WaitDuePrefix(string(instanceID))
	if err != nil {
		return nil, err
	}
	keys, err := s.store.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("list wait timers for instance %s: %w", instanceID, err)
	}
	result := make([]storage.Timer, 0, limit)
	for _, indexKey := range keys {
		_, timerKey, err := storage.ParseWaitDueKey(indexKey)
		if err != nil {
			return nil, err
		}
		encodedDue, err := s.store.Get(ctx, indexKey)
		if err != nil {
			return nil, fmt.Errorf("read wait timer due time %s: %w", indexKey, err)
		}
		dueAt, err := time.Parse(time.RFC3339Nano, string(encodedDue))
		if err != nil {
			return nil, fmt.Errorf("decode wait timer due time %s: %w", indexKey, err)
		}
		if dueAt.After(now) {
			continue
		}
		data, err := s.store.GetTimer(ctx, timerKey)
		if errors.Is(err, storage.ErrTimerNotFound) {
			// The timer was removed outside a transition that maintained the
			// index. Treat the stale pointer as absent rather than waking work.
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read wait timer %s: %w", timerKey, err)
		}
		result = append(result, storage.Timer{Key: timerKey, DueAt: dueAt.UTC(), Data: data})
		if len(result) >= limit {
			break
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].DueAt.Equal(result[j].DueAt) {
			return result[i].Key < result[j].Key
		}
		return result[i].DueAt.Before(result[j].DueAt)
	})
	return result, nil
}

// WaitTimerKeys returns the wait timer keys in one execution, optionally
// narrowed to a single capability when capabilityID is set. Prefix construction
// is delegated to storage (TimerKey/TimerPrefix/TimerPrefixForExecution), which
// is the single authority for timer key and prefix layout. The read is bounded
// to the requested scope and never lists the whole timer space. Callers use it
// for cancellation (remove every timer an execution owns) and to inspect timer
// presence in that scope; the poller consults snapshot state for deadline
// semantics rather than relying on this list as proof of a future deadline.
func (s *ExecutionStore) WaitTimerKeys(ctx context.Context, executionID shared.ID, capabilityID shared.ID) ([]string, error) {
	var prefix string
	var err error
	if capabilityID == "" {
		prefix, err = storage.TimerPrefixForExecution(string(executionID))
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

// executionDeadlineIndexKey returns the dedicated index key for an execution's
// maximum-lifetime deadline. Terminal or deadline-free executions have no key.
func executionDeadlineIndexKey(execution *Execution) (string, bool, error) {
	if execution == nil || execution.IsTerminal() {
		return "", false, nil
	}
	return executionDeadlineIndexKeyIgnoringTerminal(execution)
}

// executionDeadlineIndexKeyIgnoringTerminal derives the deadline index key from
// the execution's current deadline without consulting its status. Cleanup uses
// it when the live execution has terminalized: Adopt can index a deadline that
// exists only in memory, so a terminal snapshot has no key of its own to remove.
func executionDeadlineIndexKeyIgnoringTerminal(execution *Execution) (string, bool, error) {
	if execution == nil {
		return "", false, nil
	}
	deadline := execution.ExecutionDeadline()
	if deadline == nil || deadline.IsZero() {
		return "", false, nil
	}
	key, err := storage.ExecutionDeadlineKey(string(execution.InstanceID), *deadline, string(execution.ID))
	if err != nil {
		return "", false, fmt.Errorf("execution deadline index key for %s: %w", execution.ID, err)
	}
	return key, true, nil
}

// removeExecutionDeadlineIndexInTx removes the exact index entries named by the
// persisted snapshot and by the live execution. The live key is needed when
// Adopt indexed a deadline stamped only in memory; each key is deleted once so
// a shared key is harmless. Missing entries are tolerated so delete is
// idempotent.
func removeExecutionDeadlineIndexInTx(ctx context.Context, tx storage.KV, data []byte, liveKey string) error {
	oldKey := ""
	if len(data) > 0 {
		old, err := UnmarshalExecution(data)
		if err != nil {
			return fmt.Errorf("decode persisted execution for deadline index: %w", err)
		}
		oldKey, _, err = executionDeadlineIndexKey(old)
		if err != nil {
			return err
		}
	}
	deleted := ""
	for _, key := range []string{oldKey, liveKey} {
		if key == "" || key == deleted {
			continue
		}
		if err := tx.Delete(ctx, key); err != nil {
			return fmt.Errorf("remove execution deadline index %s: %w", key, err)
		}
		deleted = key
	}
	return nil
}

// syncExecutionDeadlineIndexInTx atomically replaces the index entry from the
// old snapshot with the one from the new execution. A crashed or legacy store
// with no entry is backfilled by the write; a stale entry is removed when the
// deadline changes, clears, or the execution reaches a terminal state.
func syncExecutionDeadlineIndexInTx(ctx context.Context, tx storage.KV, oldData []byte, execution *Execution) error {
	newKey, hasNew, err := executionDeadlineIndexKey(execution)
	if err != nil {
		return err
	}
	// Adopt can write an index entry for a deadline stamped only in memory. Once
	// the live execution is terminal there is no new key to write, and the
	// persisted snapshot still has no deadline, so derive the live key regardless
	// of status to remove the adopted entry.
	liveKey, _, err := executionDeadlineIndexKeyIgnoringTerminal(execution)
	if err != nil {
		return err
	}
	oldKey := ""
	if len(oldData) > 0 {
		old, err := UnmarshalExecution(oldData)
		if err != nil {
			return fmt.Errorf("decode persisted execution for deadline index: %w", err)
		}
		oldKey, _, err = executionDeadlineIndexKey(old)
		if err != nil {
			return err
		}
	}
	deleted := ""
	for _, staleKey := range []string{oldKey, liveKey} {
		if staleKey == "" || staleKey == newKey || staleKey == deleted {
			continue
		}
		if err := tx.Delete(ctx, staleKey); err != nil {
			return fmt.Errorf("remove stale execution deadline index %s: %w", staleKey, err)
		}
		deleted = staleKey
	}
	// Put is unconditional: an adopted legacy snapshot can have the same
	// deadline as the live execution while its index entry is still missing.
	if hasNew {
		if err := tx.Put(ctx, newKey, []byte(execution.ID)); err != nil {
			return fmt.Errorf("write execution deadline index %s: %w", newKey, err)
		}
	}
	return nil
}

// reconcileAdoptedDeadlineIndexInTx makes execution the sole owner of its
// deadline index entries. Adopt does not persist the snapshot, so a legacy
// deadline-less snapshot is re-stamped from the current clock on every restart
// and cannot identify the key written by an earlier recovery. The execution ID
// encoded in each index key is the stable anchor: every entry for this execution
// in its instance scope is removed unless it is the current key. Entries for
// other executions are left untouched.
//
// This sweep belongs to adoption only. Ordinary transitions keep using the
// bounded snapshot-to-snapshot sync and never scan the index namespace.
func reconcileAdoptedDeadlineIndexInTx(ctx context.Context, tx storage.KV, oldData []byte, execution *Execution) error {
	// The persisted snapshot may name a key outside this instance's scope, so
	// remove it first rather than relying on the scoped sweep below.
	if err := syncExecutionDeadlineIndexInTx(ctx, tx, oldData, execution); err != nil {
		return err
	}
	newKey, _, err := executionDeadlineIndexKey(execution)
	if err != nil {
		return err
	}
	prefix, err := storage.ExecutionDeadlinePrefix(string(execution.InstanceID))
	if err != nil {
		return err
	}
	keys, err := tx.List(ctx, prefix)
	if err != nil {
		return fmt.Errorf("list execution deadlines for %s: %w", execution.ID, err)
	}
	for _, key := range keys {
		if key == newKey {
			continue
		}
		_, _, ownerID, err := storage.ParseExecutionDeadlineKey(key)
		if err != nil {
			return err
		}
		if shared.ID(ownerID) != execution.ID {
			continue
		}
		if err := tx.Delete(ctx, key); err != nil {
			return fmt.Errorf("remove stale execution deadline index %s: %w", key, err)
		}
	}
	return nil
}

// writeExecutionRevision compares execution's revision with the persisted one
// and writes its next snapshot and deadline index inside tx. It is the single
// implementation of the compare-and-swap precondition shared by every
// transition path.
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
	if err := syncExecutionDeadlineIndexInTx(ctx, tx, old, execution); err != nil {
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
		if err := writeDispatchReadyIndex(ctx, tx, &item.record, item.key); err != nil {
			return err
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

const (
	dispatchIntentNamespace = "dispatch_intents"
	dispatchReadyNamespace  = "dispatch_ready"
)

func dispatchIntentKey(executionID, capabilityID, attemptID shared.ID) (string, error) {
	return storage.Key(dispatchIntentNamespace, string(executionID), string(capabilityID), string(attemptID))
}

// dispatchReadyKey is the ready-index counterpart of an intent key. The suffix
// after the namespace is identical, so discovery can map an indexed key back to
// its intent without reading an index value. An entry stays in the index while
// its intent is live, including while claimed: a crashed owner's lease must
// remain discoverable after it expires. Entries are removed when the intent
// completes or its execution reaches a terminal state.
func dispatchReadyKey(executionID, capabilityID, attemptID shared.ID) (string, error) {
	return storage.Key(dispatchReadyNamespace, string(executionID), string(capabilityID), string(attemptID))
}

func dispatchReadyPrefix() (string, error) {
	return storage.Prefix(dispatchReadyNamespace)
}

func intentKeyFromDispatchReadyKey(readyKey string) string {
	return dispatchIntentNamespace + strings.TrimPrefix(readyKey, dispatchReadyNamespace)
}

// writeDispatchReadyIndex maintains the one-to-one ready index for an intent in
// the same transaction that writes the intent itself. An unready intent has no
// index entry, so it cannot be discovered before its dependencies resolve.
func writeDispatchReadyIndex(ctx context.Context, tx storage.KV, intent *DispatchIntent, intentKey string) error {
	readyKey, err := dispatchReadyKey(intent.ExecutionID, intent.CapabilityID, intent.AttemptID)
	if err != nil {
		return fmt.Errorf("ready index key for dispatch intent %s: %w", intentKey, err)
	}
	if !intent.ReadyToRun {
		return tx.Delete(ctx, readyKey)
	}
	return tx.Put(ctx, readyKey, []byte(intentKey))
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
		if err := tx.Put(ctx, key, encoded); err != nil {
			return err
		}
		return writeDispatchReadyIndex(ctx, tx, &record, key)
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
		if err := tx.Delete(ctx, key); err != nil {
			return err
		}
		readyKey, err := dispatchReadyKey(intent.ExecutionID, intent.CapabilityID, intent.AttemptID)
		if err != nil {
			return err
		}
		return tx.Delete(ctx, readyKey)
	})
	if err != nil {
		return fmt.Errorf("complete dispatch intent %s: %w", key, err)
	}
	return nil
}

// dispatchClaimReleaseTimeout bounds the detached cleanup that releases a
// partially won batch after a claim fails. Cleanup must outlive the caller's
// context, or a cancelled reconciler would strand the claims it already owns
// until their leases expire.
const dispatchClaimReleaseTimeout = 5 * time.Second

// ClaimReadyDispatchIntents claims at most limit ready, claimable intents that
// belong to instanceID, assigning them to owner under lease. A batch may be
// under-filled: limit caps the ready-index keys examined and the execution reads
// performed in one pass, and foreign or live-lease candidates count against it.
// Later passes reach work this pass skipped. The caller must treat a returned
// batch as fully owned.
//
// Execution.InstanceID is the durable ownership authority. For each examined
// key the store reads the intent and its execution and skips the candidate
// before any claim when the execution belongs to another instance, cannot be
// loaded, or has no instance recorded. Foreign intents are never written.
// Discovery reads only the dedicated ready index, so historical intent records
// are never scanned. The store imposes no upper bound on the index, leaving
// batch sizing to deployment policy.
//
// instanceID, owner, lease and limit are validated before any discovery. Leases
// that outlived a crashed owner become claimable once they expire, while
// intents under a live lease are left untouched.
//
// Ownership is all-or-nothing. If any inspection or claim fails, every intent
// already won in this call is released with an owner-fenced write on a bounded
// detached context and the method returns (nil, err). If a release also fails,
// the returned error names both the original failure and the leaked claims;
// those claims remain lease-bounded and become claimable again when their
// leases expire.
func (s *ExecutionStore) ClaimReadyDispatchIntents(ctx context.Context, instanceID shared.ID, owner string, lease time.Duration, limit int) ([]*DispatchIntent, error) {
	if instanceID == "" {
		return nil, fmt.Errorf("instance ID is required")
	}
	if owner == "" {
		return nil, fmt.Errorf("dispatch intent owner is required")
	}
	if lease <= 0 {
		return nil, fmt.Errorf("dispatch intent lease must be positive")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("dispatch intent claim limit must be positive")
	}
	readyPrefix, err := dispatchReadyPrefix()
	if err != nil {
		return nil, fmt.Errorf("prefix ready dispatch intents: %w", err)
	}
	readyKeys, err := s.store.List(ctx, readyPrefix)
	if err != nil {
		return nil, fmt.Errorf("list ready dispatch intents: %w", err)
	}

	claimed := make([]*DispatchIntent, 0, limit)
	examined := 0
	var batchErr error
	for _, readyKey := range readyKeys {
		if examined >= limit {
			break
		}
		examined++

		intentKey := intentKeyFromDispatchReadyKey(readyKey)
		owned, err := s.dispatchIntentBelongsToInstance(ctx, intentKey, instanceID)
		if err != nil {
			batchErr = fmt.Errorf("claim dispatch intents: inspect %s: %w", intentKey, err)
			break
		}
		if !owned {
			continue
		}

		intent, won, err := s.claimDispatchIntent(ctx, intentKey, owner, lease)
		if err != nil {
			batchErr = fmt.Errorf("claim dispatch intents: %w", err)
			break
		}
		if won {
			claimed = append(claimed, intent)
		}
	}
	if batchErr != nil {
		if releaseErr := s.releaseClaimedDispatchIntents(ctx, claimed, owner); releaseErr != nil {
			return nil, fmt.Errorf("%w; release cleanup failed for %d claimed intents, which remain leased until expiry: %w",
				batchErr, len(claimed), releaseErr)
		}
		return nil, batchErr
	}
	return claimed, nil
}

// dispatchIntentBelongsToInstance reports whether the intent at intentKey has a
// durable execution owned by instanceID. It is a pre-claim ownership check: a
// foreign intent is skipped before any compare-and-swap, so no owner or lease
// field is written to another instance's intent. A missing intent or execution
// cannot be attributed and is treated as foreign; read and decode failures are
// returned so a transient error never silently drops ready work.
func (s *ExecutionStore) dispatchIntentBelongsToInstance(ctx context.Context, intentKey string, instanceID shared.ID) (bool, error) {
	data, err := s.store.Get(ctx, intentKey)
	if errors.Is(err, storage.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read dispatch intent %s: %w", intentKey, err)
	}
	var intent DispatchIntent
	if err := json.Unmarshal(data, &intent); err != nil {
		return false, fmt.Errorf("decode dispatch intent %s: %w", intentKey, err)
	}
	if intent.ExecutionID == "" {
		return false, nil
	}
	exec, err := s.Get(ctx, intent.ExecutionID)
	if errors.Is(err, storage.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read execution %s for dispatch intent %s: %w", intent.ExecutionID, intentKey, err)
	}
	return exec.InstanceID == instanceID, nil
}

// releaseClaimedDispatchIntents releases every intent in claimed behind owner's
// fence. It detaches from ctx and bounds the work so a failed claim cannot leave
// owned intents behind when the caller's context is cancelled. Every release is
// attempted so one failure cannot hide another leaked claim.
func (s *ExecutionStore) releaseClaimedDispatchIntents(ctx context.Context, claimed []*DispatchIntent, owner string) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), dispatchClaimReleaseTimeout)
	defer cancel()

	var releaseErrs []error
	for _, intent := range claimed {
		if err := s.ReleaseDispatchIntent(cleanupCtx, intent.ExecutionID, intent.CapabilityID, intent.AttemptID, owner); err != nil {
			releaseErrs = append(releaseErrs, err)
		}
	}
	return errors.Join(releaseErrs...)
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
	// InstanceID is stamped by ExecutionStore.SaveWaitTransition from the
	// execution, so scheduler writers need not know instance identity.
	InstanceID shared.ID `json:"instance_id,omitempty"`
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

// stampWaitTimerPayload records instance identity in the timer payload so a
// discovered timer can be attributed without editing scheduler writers.
func stampWaitTimerPayload(data []byte, instanceID shared.ID) ([]byte, error) {
	payload, err := DecodeWaitTimer(data)
	if err != nil {
		return nil, err
	}
	payload.InstanceID = instanceID
	stamped, err := EncodeWaitTimer(*payload)
	if err != nil {
		return nil, err
	}
	return stamped, nil
}

// putWaitDueIndexInTx records one timer in the instance-scoped due index and
// the lookup pointer used by exact deletion. A re-put of the same timer key
// removes its previous index entry in the same transaction.
func putWaitDueIndexInTx(ctx context.Context, tx storage.KV, instanceID shared.ID, timerKey string, dueAt time.Time) error {
	indexKey, err := storage.WaitDueKey(string(instanceID), timerKey)
	if err != nil {
		return err
	}
	lookupKey, err := storage.WaitDueLookupKey(timerKey)
	if err != nil {
		return err
	}
	previous, err := tx.Get(ctx, lookupKey)
	if err == nil {
		if string(previous) != indexKey {
			if err := tx.Delete(ctx, string(previous)); err != nil {
				return fmt.Errorf("remove previous wait due index %s: %w", previous, err)
			}
		}
	} else if !errors.Is(err, storage.ErrNotFound) {
		return fmt.Errorf("read wait due lookup %s: %w", lookupKey, err)
	}
	if err := tx.Put(ctx, indexKey, []byte(dueAt.UTC().Format(time.RFC3339Nano))); err != nil {
		return fmt.Errorf("write wait due index %s: %w", indexKey, err)
	}
	if err := tx.Put(ctx, lookupKey, []byte(indexKey)); err != nil {
		return fmt.Errorf("write wait due lookup %s: %w", lookupKey, err)
	}
	return nil
}

// deleteWaitDueIndexInTx removes the index entry and pointer for one timer key.
// A missing pointer is a no-op so cleanup paths remain idempotent.
func deleteWaitDueIndexInTx(ctx context.Context, tx storage.KV, timerKey string) error {
	lookupKey, err := storage.WaitDueLookupKey(timerKey)
	if err != nil {
		return err
	}
	indexKey, err := tx.Get(ctx, lookupKey)
	if errors.Is(err, storage.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read wait due lookup %s: %w", lookupKey, err)
	}
	if _, _, err := storage.ParseWaitDueKey(string(indexKey)); err != nil {
		return fmt.Errorf("decode wait due lookup %s: %w", lookupKey, err)
	}
	if err := tx.Delete(ctx, string(indexKey)); err != nil {
		return fmt.Errorf("remove wait due index %s: %w", indexKey, err)
	}
	if err := tx.Delete(ctx, lookupKey); err != nil {
		return fmt.Errorf("remove wait due lookup %s: %w", lookupKey, err)
	}
	return nil
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
	if execution.InstanceID == "" {
		return nil, fmt.Errorf("execution instance ID is required for wait transitions")
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
				if err := deleteWaitDueIndexInTx(ctx, tx, del.Key); err != nil {
					return err
				}
				continue
			}
			_, err := tx.GetTimer(ctx, del.Key)
			if errors.Is(err, storage.ErrTimerNotFound) {
				if del.RequireExisting {
					return ErrWaitTimerMissing
				}
				// A missing timer can still have a stale index entry after an
				// interrupted cleanup; remove it as part of this transition.
				if err := deleteWaitDueIndexInTx(ctx, tx, del.Key); err != nil {
					return err
				}
				continue
			}
			if err != nil {
				return fmt.Errorf("read wait timer %s: %w", del.Key, err)
			}
			if err := tx.DeleteTimer(ctx, del.Key); err != nil {
				return fmt.Errorf("delete wait timer %s: %w", del.Key, err)
			}
			if err := deleteWaitDueIndexInTx(ctx, tx, del.Key); err != nil {
				return err
			}
		}

		next, err := writeExecutionRevision(ctx, tx, key, execution)
		if err != nil {
			return err
		}
		nextRevision = next

		for _, write := range req.Effects.PutTimers {
			stamped, err := stampWaitTimerPayload(write.Data, execution.InstanceID)
			if err != nil {
				return fmt.Errorf("stamp wait timer %s: %w", write.Key, err)
			}
			if err := tx.PutTimer(ctx, write.Key, write.DueAt, stamped); err != nil {
				return fmt.Errorf("write wait timer %s: %w", write.Key, err)
			}
			if err := putWaitDueIndexInTx(ctx, tx, execution.InstanceID, write.Key, write.DueAt); err != nil {
				return err
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
		if err := writeDispatchIntents(ctx, tx, pending, createdAt); err != nil {
			return err
		}
		if execution.IsTerminal() {
			return clearDispatchReadyIndexInTx(ctx, tx, execution.ID)
		}
		return nil
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
	deleted := false
	err := s.store.WithTimer(ctx, func(tx storage.TimerTx) error {
		ok, err := tx.DeleteTimerOwned(ctx, key, owner)
		if err != nil || !ok {
			deleted = ok
			return err
		}
		if err := deleteWaitDueIndexInTx(ctx, tx, key); err != nil {
			return err
		}
		deleted = true
		return nil
	})
	return deleted, err
}
