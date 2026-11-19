package storage

import (
	"runtime"
	"sync"
	"sync/atomic"
)

// TransactionGate serializes transactions against each other and against plain
// store operations, while still rejecting re-entrant calls made from inside the
// transaction callback.
//
// Concurrent callers wait for the active transaction to finish. Only the
// goroutine that owns the transaction gets ErrNestedTransaction or
// ErrTransactionActive, because it is already inside the callback and would
// otherwise deadlock waiting for itself.
type TransactionGate struct {
	mu    sync.RWMutex
	owner atomic.Uint64
}

// Begin starts an exclusive transaction. It returns ErrNestedTransaction when
// the caller is already inside a transaction; concurrent transactions wait for
// the active one to finish.
func (g *TransactionGate) Begin() error {
	if g.mu.TryLock() {
		g.owner.Store(goroutineID())
		return nil
	}
	if g.owner.Load() == goroutineID() {
		return ErrNestedTransaction
	}
	g.mu.Lock()
	g.owner.Store(goroutineID())
	return nil
}

// End releases the transaction started by Begin.
func (g *TransactionGate) End() {
	g.owner.Store(0)
	g.mu.Unlock()
}

// BeginRead starts a plain store operation. It returns ErrTransactionActive
// when the caller is inside the transaction callback; concurrent operations
// wait until the transaction finishes.
func (g *TransactionGate) BeginRead() error {
	if g.mu.TryRLock() {
		return nil
	}
	if g.owner.Load() == goroutineID() {
		return ErrTransactionActive
	}
	g.mu.RLock()
	return nil
}

// EndRead releases a plain store operation started by BeginRead.
func (g *TransactionGate) EndRead() {
	g.mu.RUnlock()
}

// Active reports whether a transaction is currently owned.
func (g *TransactionGate) Active() bool {
	return g.owner.Load() != 0
}

// goroutineID returns the current goroutine's identifier. It is used only to
// distinguish a re-entrant call from a concurrent one, so the cost is paid only
// when the gate is already held.
func goroutineID() uint64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	const prefix = "goroutine "
	if n <= len(prefix) {
		return 0
	}
	var id uint64
	for _, b := range buf[len(prefix):n] {
		if b < '0' || b > '9' {
			break
		}
		id = id*10 + uint64(b-'0')
	}
	return id
}
