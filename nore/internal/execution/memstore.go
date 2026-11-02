package execution

import (
	"context"
	"fmt"
	"sync"

	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/shared/types/core"
)

// MemoryStore is the in-process execution repository. It implements the same
// boundary as ExecutionStore so scheduler and engine tests exercise the real
// interface instead of a looser test-only shape.
type MemoryStore struct {
	mu         sync.RWMutex
	executions map[core.ID]*Execution
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{executions: make(map[core.ID]*Execution)}
}

func (s *MemoryStore) Add(_ context.Context, execution *Execution) error {
	if execution == nil {
		return fmt.Errorf("execution is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.executions[execution.ID]; exists {
		return fmt.Errorf("%w: execution %s", storage.ErrAlreadyExists, execution.ID)
	}
	s.executions[execution.ID] = execution
	return nil
}

func (s *MemoryStore) Get(_ context.Context, executionID core.ID) (*Execution, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	execution, exists := s.executions[executionID]
	if !exists {
		return nil, fmt.Errorf("%w: execution %s", storage.ErrNotFound, executionID)
	}
	return execution, nil
}

// Save records the current state of an execution, inserting it when it is not
// already held.
//
// Save is an upsert rather than an update because it is also how a caller
// persists an execution it read back from durable storage. An execution loaded
// by Get is not in memory, so an update-only Save would reject exactly the
// executions a recovering process most needs to write back. Add remains the
// insert-only operation that rejects a duplicate ID.
func (s *MemoryStore) Save(_ context.Context, execution *Execution) error {
	if execution == nil {
		return fmt.Errorf("execution is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.executions[execution.ID] = execution
	return nil
}

func (s *MemoryStore) Delete(_ context.Context, executionID core.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.executions, executionID)
	return nil
}

func (s *MemoryStore) List(_ context.Context) ([]*Execution, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	list := make([]*Execution, 0, len(s.executions))
	for _, exec := range s.executions {
		list = append(list, exec)
	}
	return list, nil
}

// ListByInstance returns every execution that belongs to instanceID. The
// persistent store applies the same ownership predicate to its merged view.
func (s *MemoryStore) ListByInstance(_ context.Context, instanceID core.ID) ([]*Execution, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	list := make([]*Execution, 0)
	for _, exec := range s.executions {
		if exec.InstanceID == instanceID {
			list = append(list, exec)
		}
	}
	return list, nil
}
