package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

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
		currentRevision, err := storedRevision(ctx, tx, key)
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
		return tx.Put(ctx, key, data)
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

func storedRevision(ctx context.Context, tx storage.Tx, key string) (uint64, error) {
	data, err := tx.Get(ctx, key)
	if errors.Is(err, storage.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read persisted execution revision: %w", err)
	}
	var record struct {
		Revision uint64 `json:"revision"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return 0, fmt.Errorf("decode persisted execution revision: %w", err)
	}
	return record.Revision, nil
}

func marshalExecutionAt(execution *Execution, revision uint64) ([]byte, error) {
	return json.Marshal(execution.snapshotAt(revision))
}
