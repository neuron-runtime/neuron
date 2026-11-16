package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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

func (s *MemoryStore) CompareAndSwap(ctx context.Context, key string, old, new []byte) error {
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
	current, ok := s.data[key]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	if !bytes.Equal(current, old) {
		return fmt.Errorf("%w: %s", ErrConflict, key)
	}
	s.data[key] = append([]byte(nil), new...)
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

// ClaimTimer atomically claims a timer lease. Returns (claimed, error).
// If the timer key does not exist, it is created with the owner and lease.
// If the timer key already exists, the owner is updated and the lease is extended.
// The function returns true if the claim succeeded, false otherwise.
func (s *MemoryStore) ClaimTimer(ctx context.Context, timerKey string, owner string, lease time.Duration) (bool, error) {
	now := time.Now().Unix()
	if val, ok := s.data[timerKey]; ok {
		// Timer exists - update owner and lease expiry
		var rec timerRecord
		if err := json.Unmarshal(val, &rec); err != nil {
			// If we can't parse, create a minimal record
			rec = timerRecord{
				WaitUntil:      0,
				CreatedAt:      now,
				Owner:          owner,
				LeaseExpiresAt: now + int64(lease.Seconds()),
				WaitType:       "",
				ExpectedSignal: "",
			}
		} else {
			rec.Owner = owner
			rec.LeaseExpiresAt = now + int64(lease.Seconds())
		}
		newData, err := json.Marshal(rec)
		if err != nil {
			return false, err
		}
		s.data[timerKey] = newData
		return true, nil
	} else {
		// Timer doesn't exist - create new with owner and lease
		rec := timerRecord{
			WaitUntil:      0,
			CreatedAt:      now,
			Owner:          owner,
			LeaseExpiresAt: now + int64(lease.Seconds()),
			WaitType:       "",
			ExpectedSignal: "",
		}
		newData, err := json.Marshal(rec)
		if err != nil {
			return false, err
		}
		s.data[timerKey] = newData
		return true, nil
	}
}

// DeleteTimer deletes the timer record at the given key.
func (s *MemoryStore) DeleteTimer(ctx context.Context, timerKey string) error {
	if _, ok := s.data[timerKey]; ok {
		delete(s.data, timerKey)
		return nil
	}
	return fmt.Errorf("%w: %s", ErrTimerNotFound, timerKey)
}

// GetTimer retrieves the data stored at the given timer key.
func (s *MemoryStore) GetTimer(ctx context.Context, timerKey string) ([]byte, error) {
	if val, ok := s.data[timerKey]; ok {
		return append([]byte(nil), val...), nil
	}
	return nil, fmt.Errorf("%w: %s", ErrTimerNotFound, timerKey)
}

// ListTimers returns all timer keys matching the given prefix.
func (s *MemoryStore) ListTimers(ctx context.Context, prefix string) ([]string, error) {
	keys := make([]string, 0)
	for key := range s.data {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

// PutTimer stores data at the given timer key. If the key already exists,
// the write is an overwrite (compare-and-swap with existing value).
func (s *MemoryStore) PutTimer(ctx context.Context, timerKey string, data []byte) error {
	s.data[timerKey] = append([]byte(nil), data...)
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

func (t memoryTx) CompareAndSwap(ctx context.Context, key string, old, new []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	current, ok := t.store.data[key]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	if !bytes.Equal(current, old) {
		return fmt.Errorf("%w: %s", ErrConflict, key)
	}
	t.store.data[key] = append([]byte(nil), new...)
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

// timerRecord represents a timer record stored as JSON bytes.
// The JSON fields are: WaitUntil, CreatedAt, Owner, LeaseExpiresAt, WaitType, ExpectedSignal.
type timerRecord struct {
	WaitUntil      int64  `json:"waitUntil"`
	CreatedAt      int64  `json:"createdAt"`
	Owner          string `json:"owner"`
	LeaseExpiresAt int64  `json:"leaseExpiresAt"`
	WaitType       string `json:"waitType"`
	ExpectedSignal string `json:"expectedSignal"`
}

// WithTimer runs fn inside one timer transaction. Returning an error from fn
// rolls the transaction back. WithTimer is analogous to WithTx but operates
// on the timer surface.
func (s *MemoryStore) WithTimer(ctx context.Context, fn func(TimerTx) error) error {
	if err := s.guard(); err != nil {
		return err
	}
	if fn == nil {
		return fmt.Errorf("timer transaction callback is required")
	}
	if err := s.gate.Begin(); err != nil {
		return err
	}
	defer s.gate.End()

	s.mu.Lock()
	defer s.mu.Unlock()

	timerTx := memoryTimerTx{store: s}
	if err := fn(timerTx); err != nil {
		s.data = snapshotMemoryData(s.data)
		return err
	}
	if err := ctx.Err(); err != nil {
		s.data = snapshotMemoryData(s.data)
		return err
	}
	return nil
}

// memoryTimerTx operates on the store's map while WithTimer holds the data lock.
type memoryTimerTx struct {
	store *MemoryStore
}

func (t memoryTimerTx) GetTimer(ctx context.Context, timerKey string) ([]byte, error) {
	if val, ok := t.store.data[timerKey]; ok {
		return append([]byte(nil), val...), nil
	}
	return nil, fmt.Errorf("%w: %s", ErrTimerNotFound, timerKey)
}

func (t memoryTimerTx) PutTimer(ctx context.Context, timerKey string, data []byte) error {
	if _, ok := t.store.data[timerKey]; ok {
		t.store.data[timerKey] = append([]byte(nil), data...)
	} else {
		t.store.data[timerKey] = append([]byte(nil), data...)
	}
	return nil
}

func (t memoryTimerTx) DeleteTimer(ctx context.Context, timerKey string) error {
	delete(t.store.data, timerKey)
	return nil
}

func (t memoryTimerTx) List(ctx context.Context, prefix string) ([]string, error) {
	keys := make([]string, 0)
	for key := range t.store.data {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

func (t memoryTimerTx) Get(ctx context.Context, key string) ([]byte, error) {
	if val, ok := t.store.data[key]; ok {
		return append([]byte(nil), val...), nil
	}
	return nil, ErrNotFound
}

func (t memoryTimerTx) Put(ctx context.Context, key string, data []byte) error {
	t.store.data[key] = append([]byte(nil), data...)
	return nil
}

func (t memoryTimerTx) Delete(ctx context.Context, key string) error {
	delete(t.store.data, key)
	return nil
}

func (t memoryTimerTx) CompareAndSwap(ctx context.Context, key string, old, new []byte) error {
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	val, ok := t.store.data[key]
	if !ok {
		return ErrNotFound
	}
	if !bytes.Equal(val, old) {
		return ErrConflict
	}
	t.store.data[key] = append([]byte(nil), new...)
	return nil
}

func (t memoryTimerTx) ListTimers(ctx context.Context, prefix string) ([]string, error) {
	keys := make([]string, 0)
	for key := range t.store.data {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

func (t memoryTimerTx) ClaimTimer(ctx context.Context, timerKey string, owner string, lease time.Duration) (bool, error) {
	now := time.Now().Unix()
	if val, ok := t.store.data[timerKey]; ok {
		// Timer exists - update owner and lease expiry
		var rec timerRecord
		if err := json.Unmarshal(val, &rec); err != nil {
			// If we can't parse, create a minimal record
			rec = timerRecord{
				WaitUntil:      0,
				CreatedAt:      now,
				Owner:          owner,
				LeaseExpiresAt: now + int64(lease.Seconds()),
				WaitType:       "",
				ExpectedSignal: "",
			}
		} else {
			rec.Owner = owner
			rec.LeaseExpiresAt = now + int64(lease.Seconds())
		}
		newData, err := json.Marshal(rec)
		if err != nil {
			return false, err
		}
		t.store.data[timerKey] = newData
		return true, nil
	} else {
		// Timer doesn't exist - create new with owner and lease
		rec := timerRecord{
			WaitUntil:      0,
			CreatedAt:      now,
			Owner:          owner,
			LeaseExpiresAt: now + int64(lease.Seconds()),
			WaitType:       "",
			ExpectedSignal: "",
		}
		newData, err := json.Marshal(rec)
		if err != nil {
			return false, err
		}
		t.store.data[timerKey] = newData
		return true, nil
	}
}

// snapshotMemoryData returns a shallow copy of the store's data map for
// transaction rollback semantics.
func snapshotMemoryData(src map[string][]byte) map[string][]byte {
	dst := make(map[string][]byte, len(src))
	for key, value := range src {
		dst[key] = append([]byte(nil), value...)
	}
	return dst
}
