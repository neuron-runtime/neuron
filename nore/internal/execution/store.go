package execution

import (
	"context"
	"fmt"
	"sync"

	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/nore/internal/storage/sqlite"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

type ExecutionStore struct {
	mu    sync.RWMutex
	mem   *MemoryStore
	store storage.Store
}

func NewExecutionStore(store storage.Store) *ExecutionStore {
	return &ExecutionStore{
		mem:   NewMemoryStore(),
		store: store,
	}
}

func (s *ExecutionStore) Add(execution *Execution) error {
	if err := s.mem.Add(execution); err != nil {
		return err
	}

	data, err := MarshalExecution(execution)
	if err != nil {
		return fmt.Errorf("marshal execution: %w", err)
	}

	if err := s.store.Put(context.Background(), s.key(execution.ID), data); err != nil {
		return fmt.Errorf("persist execution: %w", err)
	}

	return nil
}

func (s *ExecutionStore) Get(executionID shared.ID) (*Execution, bool) {
	exec, ok := s.mem.Get(executionID)
	if ok {
		return exec, true
	}

	return s.loadFromStore(executionID)
}

func (s *ExecutionStore) Save(ctx context.Context, execution *Execution) error {
	if err := s.mem.Save(ctx, execution); err != nil {
		return err
	}

	data, err := MarshalExecution(execution)
	if err != nil {
		return fmt.Errorf("marshal execution: %w", err)
	}

	if err := s.store.Put(ctx, s.key(execution.ID), data); err != nil {
		return fmt.Errorf("persist execution: %w", err)
	}

	return nil
}

func (s *ExecutionStore) Delete(executionID shared.ID) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.mem.Delete(executionID)
	_ = s.store.Delete(context.Background(), s.key(executionID))

}

func (s *ExecutionStore) List() []*Execution {
	s.mu.RLock()
	keys, err := s.store.List(context.Background(), "executions/")
	s.mu.RUnlock()

	if err != nil {
		return s.mem.List()
	}

	result := make([]*Execution, 0, len(keys))
	seen := make(map[shared.ID]bool)

	live := s.mem.List()
	for _, exec := range live {
		result = append(result, exec)
		seen[exec.ID] = true
	}

	for _, key := range keys {
		id := shared.ID(extractID(key))
		if seen[id] {
			continue
		}
		exec, ok := s.loadFromStore(id)
		if ok {
			result = append(result, exec)
		}
	}

	return result
}

func (s *ExecutionStore) ListByInstance(instanceID shared.ID) []*Execution {
	s.mu.RLock()
	keys, err := s.store.List(context.Background(), "executions/")
	s.mu.RUnlock()

	result := s.mem.ListByInstance(instanceID)
	seen := make(map[shared.ID]bool, len(result))
	for _, exec := range result {
		seen[exec.ID] = true
	}

	if err != nil {
		return result
	}
	for _, key := range keys {
		id := shared.ID(extractID(key))
		if seen[id] {
			continue
		}
		exec, ok := s.loadFromStore(id)
		if !ok || exec.InstanceID != instanceID {
			continue
		}
		result = append(result, exec)
	}
	return result
}

func (s *ExecutionStore) loadFromStore(executionID shared.ID) (*Execution, bool) {
	data, err := s.store.Get(context.Background(), s.key(executionID))
	if err != nil {
		return nil, false
	}

	exec, err := UnmarshalExecution(data)
	if err != nil {
		return nil, false
	}

	return exec, true
}

func (s *ExecutionStore) key(id shared.ID) string {
	return sqlite.SanitizeKey("executions", string(id))
}

func extractID(key string) string {
	prefix := "executions/"
	if len(key) > len(prefix) {
		return key[len(prefix):]
	}
	return key
}
