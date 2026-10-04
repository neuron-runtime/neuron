package execution

import (
	"context"
	"fmt"
	"sync"

	"github.com/neuron-runtime/neuron/shared/types/core"
)

type MemoryStore struct {
	mu         sync.RWMutex
	executions map[core.ID]*Execution
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{executions: make(map[core.ID]*Execution)}
}

func (s *MemoryStore) Add(execution *Execution) error {
	if execution == nil {
		return fmt.Errorf("execution is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.executions[execution.ID]; exists {
		return fmt.Errorf("execution %s already exists", execution.ID)
	}
	s.executions[execution.ID] = execution
	return nil
}

func (s *MemoryStore) Get(executionID core.ID) (*Execution, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	execution, exists := s.executions[executionID]
	return execution, exists
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

func (s *MemoryStore) Delete(executionID core.ID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.executions, executionID)
}

func (s *MemoryStore) List() []*Execution {
	s.mu.RLock()
	defer s.mu.RUnlock()

	list := make([]*Execution, 0, len(s.executions))
	for _, exec := range s.executions {
		list = append(list, exec)
	}
	return list
}

// ListByInstance returns every execution that belongs to instanceID. It is the
// single implementation of the ownership filter, shared with the persistent
// store so the two can never disagree about which executions an instance owns.
func (s *MemoryStore) ListByInstance(instanceID core.ID) []*Execution {
	s.mu.RLock()
	defer s.mu.RUnlock()

	list := make([]*Execution, 0)
	for _, exec := range s.executions {
		if exec.InstanceID == instanceID {
			list = append(list, exec)
		}
	}
	return list
}
