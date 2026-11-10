package storage

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// MemoryStore is a process-local Store used for execution durability "none".
// It implements the same transaction and re-entrancy semantics as persistent
// backends so execution and event persistence can run unchanged, while nothing
// survives the process.
//
// MemoryStore is not registered with the driver registry: it is an internal
// runtime detail, not a storage backend an operator can select.
type MemoryStore struct {
	gate   TransactionGate
	mu     sync.RWMutex
	data   map[string][]byte
	closed atomic.Bool
}

// NewMemoryStore returns an empty, open in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{data: make(map[string][]byte)}
}

func (s *MemoryStore) Get(ctx context.Context, key string) ([]byte, error) {
	if err := s.guard(); err != nil {
		return nil, err
	}
	if err := s.gate.BeginRead(); err != nil {
		return nil, err
	}
	defer s.gate.EndRead()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.data[key]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	return append([]byte(nil), value...), nil
}

func (s *MemoryStore) Put(ctx context.Context, key string, data []byte) error {
	if err := s.guard(); err != nil {
		return err
	}
	if err := s.gate.BeginRead(); err != nil {
		return err
	}
	defer s.gate.EndRead()
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = append([]byte(nil), data...)
	return nil
}

func (s *MemoryStore) Delete(ctx context.Context, key string) error {
	if err := s.guard(); err != nil {
		return err
	}
	if err := s.gate.BeginRead(); err != nil {
		return err
	}
	defer s.gate.EndRead()
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, key)
	return nil
}

func (s *MemoryStore) List(ctx context.Context, prefix string) ([]string, error) {
	if err := s.guard(); err != nil {
		return nil, err
	}
	if err := s.gate.BeginRead(); err != nil {
		return nil, err
	}
	defer s.gate.EndRead()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0)
	for key := range s.data {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

func (s *MemoryStore) Ping(ctx context.Context) error {
	if err := s.guard(); err != nil {
		return err
	}
	if err := s.gate.BeginRead(); err != nil {
		return err
	}
	defer s.gate.EndRead()
	return ctx.Err()
}

func (s *MemoryStore) WithTx(ctx context.Context, fn func(Tx) error) error {
	if err := s.guard(); err != nil {
		return err
	}
	if fn == nil {
		return fmt.Errorf("transaction callback is required")
	}
	if err := s.gate.Begin(); err != nil {
		return err
	}
	defer s.gate.End()

	s.mu.Lock()
	defer s.mu.Unlock()

	snapshot := cloneMemoryData(s.data)
	if err := fn(memoryTx{store: s}); err != nil {
		s.data = snapshot
		return err
	}
	if err := ctx.Err(); err != nil {
		s.data = snapshot
		return err
	}
	return nil
}

func (s *MemoryStore) Close() error {
	if err := s.guard(); err != nil {
		return err
	}
	if s.gate.Active() {
		return ErrTransactionActive
	}
	if err := s.gate.Begin(); err != nil {
		return err
	}
	defer s.gate.End()

	if s.closed.Swap(true) {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = nil
	return nil
}

func (s *MemoryStore) guard() error {
	if s.closed.Load() {
		return ErrClosed
	}
	return nil
}

// memoryTx operates on the store's map while WithTx holds both the transaction
// gate and the data lock, so it must not acquire either again.
type memoryTx struct {
	store *MemoryStore
}

func (t memoryTx) Get(ctx context.Context, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	value, ok := t.store.data[key]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	return append([]byte(nil), value...), nil
}

func (t memoryTx) Put(ctx context.Context, key string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t.store.data[key] = append([]byte(nil), data...)
	return nil
}

func (t memoryTx) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	delete(t.store.data, key)
	return nil
}

func (t memoryTx) List(ctx context.Context, prefix string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	keys := make([]string, 0)
	for key := range t.store.data {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

func cloneMemoryData(src map[string][]byte) map[string][]byte {
	dst := make(map[string][]byte, len(src))
	for key, value := range src {
		dst[key] = append([]byte(nil), value...)
	}
	return dst
}
