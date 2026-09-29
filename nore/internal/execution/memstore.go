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

func (s *MemoryStore) Save(_ context.Context, execution *Execution) error {
	if execution == nil {
		return fmt.Errorf("execution is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.executions[execution.ID]; !exists {
		return fmt.Errorf("execution %s not found", execution.ID)
	}
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
