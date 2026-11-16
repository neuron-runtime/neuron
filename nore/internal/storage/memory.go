package storage

import (
	"bytes"
	"context"
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
	timers map[string]timerEntry
	closed atomic.Bool
}

type timerEntry struct {
	DueAt          time.Time
	Owner          string
	LeaseExpiresAt time.Time
	Data           []byte
}

// NewMemoryStore returns an empty, open in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		data:   make(map[string][]byte),
		timers: make(map[string]timerEntry),
	}
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
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.gate.Begin(); err != nil {
		return err
	}
	defer s.gate.End()

	s.mu.Lock()
	defer s.mu.Unlock()

	snapshot := cloneMemoryData(s.data)
	rolledBack := false
	rollback := func() {
		if rolledBack {
			return
		}
		s.data = snapshot
		rolledBack = true
	}
	defer func() {
		if recover() != nil {
			rollback()
			panic(recover())
		}
	}()
	if err := fn(memoryTx{store: s}); err != nil {
		rollback()
		return err
	}
	if err := ctx.Err(); err != nil {
		rollback()
		return err
	}
	return nil
}

func (s *MemoryStore) WithTimer(ctx context.Context, fn func(TimerTx) error) error {
	if err := s.guard(); err != nil {
		return err
	}
	if fn == nil {
		return fmt.Errorf("timer transaction callback is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.gate.Begin(); err != nil {
		return err
	}
	defer s.gate.End()

	s.mu.Lock()
	defer s.mu.Unlock()

	dataSnapshot := cloneMemoryData(s.data)
	timerSnapshot := cloneTimerData(s.timers)
	rolledBack := false
	rollback := func() {
		if rolledBack {
			return
		}
		s.data = dataSnapshot
		s.timers = timerSnapshot
		rolledBack = true
	}
	defer func() {
		if recover() != nil {
			rollback()
			panic(recover())
		}
	}()
	if err := fn(memoryTimerTx{store: s}); err != nil {
		rollback()
		return err
	}
	if err := ctx.Err(); err != nil {
		rollback()
		return err
	}
	return nil
}

func (s *MemoryStore) PutTimer(ctx context.Context, timerKey string, dueAt time.Time, data []byte) error {
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
	s.timers[timerKey] = timerEntry{DueAt: dueAt, Data: append([]byte(nil), data...)}
	return nil
}

func (s *MemoryStore) GetTimer(ctx context.Context, timerKey string) ([]byte, error) {
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
	entry, ok := s.timers[timerKey]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrTimerNotFound, timerKey)
	}
	return append([]byte(nil), entry.Data...), nil
}

func (s *MemoryStore) DeleteTimer(ctx context.Context, timerKey string) error {
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
	delete(s.timers, timerKey)
	return nil
}

func (s *MemoryStore) ListTimers(ctx context.Context, prefix string) ([]string, error) {
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
	return listTimerKeysLocked(s.timers, prefix), nil
}

func (s *MemoryStore) ClaimTimer(ctx context.Context, timerKey string, owner string, lease time.Duration) (bool, error) {
	if err := s.guard(); err != nil {
		return false, err
	}
	if err := s.gate.BeginRead(); err != nil {
		return false, err
	}
	defer s.gate.EndRead()
	if err := ctx.Err(); err != nil {
		return false, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	return claimTimerLocked(s.timers, timerKey, owner, lease, time.Now())
}

func (s *MemoryStore) DueTimers(ctx context.Context, now time.Time, limit int) ([]Timer, error) {
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
	return dueTimersLocked(s.timers, now, limit), nil
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
	s.timers = nil
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

// memoryTimerTx operates on the store maps while WithTimer holds the data lock.
type memoryTimerTx struct {
	store *MemoryStore
}

func (t memoryTimerTx) Get(ctx context.Context, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	value, ok := t.store.data[key]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	return append([]byte(nil), value...), nil
}

func (t memoryTimerTx) Put(ctx context.Context, key string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t.store.data[key] = append([]byte(nil), data...)
	return nil
}

func (t memoryTimerTx) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	delete(t.store.data, key)
	return nil
}

func (t memoryTimerTx) CompareAndSwap(ctx context.Context, key string, old, new []byte) error {
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

func (t memoryTimerTx) List(ctx context.Context, prefix string) ([]string, error) {
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

func (t memoryTimerTx) GetTimer(ctx context.Context, timerKey string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entry, ok := t.store.timers[timerKey]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrTimerNotFound, timerKey)
	}
	return append([]byte(nil), entry.Data...), nil
}

func (t memoryTimerTx) PutTimer(ctx context.Context, timerKey string, dueAt time.Time, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t.store.timers[timerKey] = timerEntry{DueAt: dueAt, Data: append([]byte(nil), data...)}
	return nil
}

func (t memoryTimerTx) DeleteTimer(ctx context.Context, timerKey string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	delete(t.store.timers, timerKey)
	return nil
}

func (t memoryTimerTx) ListTimers(ctx context.Context, prefix string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return listTimerKeysLocked(t.store.timers, prefix), nil
}

func (t memoryTimerTx) ClaimTimer(ctx context.Context, timerKey string, owner string, lease time.Duration) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return claimTimerLocked(t.store.timers, timerKey, owner, lease, time.Now())
}

func (t memoryTimerTx) DueTimers(ctx context.Context, now time.Time, limit int) ([]Timer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return dueTimersLocked(t.store.timers, now, limit), nil
}

func cloneMemoryData(src map[string][]byte) map[string][]byte {
	dst := make(map[string][]byte, len(src))
	for key, value := range src {
		dst[key] = append([]byte(nil), value...)
	}
	return dst
}

func cloneTimerData(src map[string]timerEntry) map[string]timerEntry {
	dst := make(map[string]timerEntry, len(src))
	for key, value := range src {
		dst[key] = timerEntry{
			DueAt:          value.DueAt,
			Owner:          value.Owner,
			LeaseExpiresAt: value.LeaseExpiresAt,
			Data:           append([]byte(nil), value.Data...),
		}
	}
	return dst
}

func listTimerKeysLocked(timers map[string]timerEntry, prefix string) []string {
	keys := make([]string, 0)
	for key := range timers {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func leaseLive(entry timerEntry, now time.Time) bool {
	return entry.Owner != "" && !entry.LeaseExpiresAt.IsZero() && entry.LeaseExpiresAt.After(now)
}

func claimTimerLocked(timers map[string]timerEntry, timerKey string, owner string, lease time.Duration, now time.Time) (bool, error) {
	entry, ok := timers[timerKey]
	if !ok {
		return false, nil
	}
	if entry.DueAt.After(now) {
		return false, nil
	}
	if leaseLive(entry, now) {
		return false, nil
	}
	entry.Owner = owner
	entry.LeaseExpiresAt = now.Add(lease)
	timers[timerKey] = entry
	return true, nil
}

func dueTimersLocked(timers map[string]timerEntry, now time.Time, limit int) []Timer {
	if limit <= 0 {
		return []Timer{}
	}
	out := make([]Timer, 0)
	for key, entry := range timers {
		if entry.DueAt.After(now) {
			continue
		}
		if leaseLive(entry, now) {
			continue
		}
		out = append(out, Timer{
			Key:            key,
			DueAt:          entry.DueAt,
			Owner:          entry.Owner,
			LeaseExpiresAt: entry.LeaseExpiresAt,
			Data:           append([]byte(nil), entry.Data...),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DueAt.Equal(out[j].DueAt) {
			return out[i].Key < out[j].Key
		}
		return out[i].DueAt.Before(out[j].DueAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}
