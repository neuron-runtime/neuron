package storage

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrNotFound reports that a key does not exist. Callers must use errors.Is
	// rather than matching an error string so a backend can wrap the sentinel
	// with its own context.
	ErrNotFound = errors.New("storage: key not found")

	// ErrConflict reports that a compare-and-swap write lost to another writer.
	// The caller must reload the record before retrying.
	ErrConflict = errors.New("storage: write conflict")

	// ErrAlreadyExists reports that an insert-only write found an existing
	// record.
	ErrAlreadyExists = errors.New("storage: key already exists")

	// ErrNestedTransaction reports a WithTx call made from inside another
	// transaction. Transactions do not compose implicitly; the caller already
	// holds the transaction and must pass its Tx to nested work.
	ErrNestedTransaction = errors.New("storage: nested transaction")

	// ErrTransactionActive reports a plain Store operation attempted from
	// inside a WithTx callback. Use the callback's Tx so the operation
	// participates in the same atomic unit.
	ErrTransactionActive = errors.New("storage: transaction already active")

	// ErrUnknownDriver reports a storage driver that no backend registered.
	ErrUnknownDriver = errors.New("storage: unknown driver")

	// ErrInvalidKey reports a key segment that cannot be represented safely.
	ErrInvalidKey = errors.New("storage: invalid key")

	// ErrClosed reports an operation on a store that has been closed.
	ErrClosed = errors.New("storage: closed")

	// ErrTimerNotFound reports that a timer key was not found.
	ErrTimerNotFound = errors.New("storage: timer not found")

	// ErrTimerConflict reports that a compare-and-swap write lost to another writer
	// on a timer. The caller must reload the record before retrying.
	ErrTimerConflict = errors.New("storage: timer write conflict")

	// ErrTimerAlreadyExists reports that an insert-only write found an existing
	// timer record.
	ErrTimerAlreadyExists = errors.New("storage: timer already exists")

	// ErrTimerLeased reports that a timer is held under a live lease and cannot
	// be overwritten by PutTimer.
	ErrTimerLeased = errors.New("storage: timer leased")
	// ErrTimerOwnerEmpty reports that a timer claim/fencing owner is required and
	// must be a non-empty, opaque per-claim token.
	ErrTimerOwnerEmpty = errors.New("storage: timer owner required")
)

// KV is the key/value surface shared by a Store and a transaction. Keys are
// opaque strings; callers construct them with Key or Prefix.
type KV interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Put(ctx context.Context, key string, data []byte) error
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) ([]string, error)

	// CompareAndSwap atomically replaces the value stored at key when its
	// current value equals old. It returns ErrNotFound when key is missing and
	// ErrConflict when the stored value differs from old; in both cases the
	// stored value is left unchanged. On success new becomes the stored value.
	//
	// old and new are opaque bytes compared and stored byte-for-byte. The
	// operation participates in the surrounding transaction when called on a
	// Tx, so a conflict or a later callback error rolls back exactly like any
	// other transactional write.
	CompareAndSwap(ctx context.Context, key string, old, new []byte) error
}

// Tx is a KV bound to one transaction. Every operation on a Tx participates in
// the transaction opened by Store.WithTx.
type Tx interface {
	KV
}

// Timer is the durable, indexed timer substrate record. DueAt is the scheduling
// time used for discovery; Owner and LeaseExpiresAt form the claim lease; Data
// carries the opaque timer payload.
type Timer struct {
	Key            string
	DueAt          time.Time
	Owner          string
	LeaseExpiresAt time.Time
	Data           []byte
}

// TimerTx is a transaction surface for timer operations. Every operation on a
// TimerTx participates in the timer transaction opened by Store.WithTimer.
// TimerTx.KV addresses the main KV surface on every backend.
type TimerTx interface {
	KV

	// GetTimer retrieves the data stored at the given timer key.
	GetTimer(ctx context.Context, timerKey string) ([]byte, error)
	// PutTimer stores data at the given timer key with an explicit due time.
	// Overwriting an existing timer held under a live lease returns ErrTimerLeased.
	// Re-putting an unleased or expired-lease timer is allowed and clears stale
	// owner/lease.
	PutTimer(ctx context.Context, timerKey string, dueAt time.Time, data []byte) error
	// DeleteTimer deletes the timer record at the given key. It is unfenced and
	// intended for admin/cleanup; pollers should prefer DeleteTimerOwned.
	DeleteTimer(ctx context.Context, timerKey string) error
	// DeleteTimerOwned deletes the timer record only when it exists and is owned
	// by owner. It returns (false, nil) when the timer is missing or owned by a
	// different owner.
	DeleteTimerOwned(ctx context.Context, timerKey string, owner string) (bool, error)
	// ListTimers returns all timer keys matching the given prefix.
	ListTimers(ctx context.Context, prefix string) ([]string, error)
	// ClaimTimer atomically claims a timer lease. It claims only when the timer
	// exists, is due at or before now, and is unowned or its lease is expired.
	// Owner is an opaque per-claim fencing token and must be non-empty; an empty
	// owner returns ErrTimerOwnerEmpty. It returns (false, nil) for a missing
	// timer, a not-yet-due timer, or a timer held by a live lease.
	ClaimTimer(ctx context.Context, timerKey string, owner string, lease time.Duration) (bool, error)
	// RenewTimer extends the lease only when the record exists, is owned by owner,
	// and the lease is still live (not expired). Owner must be non-empty; an empty
	// owner returns ErrTimerOwnerEmpty. It returns (false, nil) otherwise.
	RenewTimer(ctx context.Context, timerKey string, owner string, lease time.Duration) (bool, error)
	// DueTimers returns at most limit due, unleased timers ordered by due time
	// then key. A timer is due when due_at <= now and its lease is unowned or
	// expired.
	DueTimers(ctx context.Context, now time.Time, limit int) ([]Timer, error)
}

// Store is the only storage type runtime components receive. It exposes plain
// reads and writes, health checking, explicit transactions, and lifecycle
// closure. Backend construction is owned by Open; components must not construct
// a backend themselves.
type Store interface {
	KV

	// Ping verifies that the backend is reachable.
	Ping(ctx context.Context) error

	// WithTx runs fn inside one transaction. Returning an error from fn rolls
	// the transaction back. Concurrent WithTx calls wait for the active
	// transaction; a WithTx call from inside fn returns ErrNestedTransaction.
	// Plain Store operations from inside fn return ErrTransactionActive;
	// concurrent plain operations wait for the transaction to finish.
	WithTx(ctx context.Context, fn func(Tx) error) error

	// Close releases the backend. Close during an active transaction returns
	// ErrTransactionActive.
	Close() error

	// Timer operations for Phase 5 (signals, timers, and long-running execution).

	// WithTimer runs fn inside one timer transaction over the main KV surface and
	// the timer surface. Returning an error from fn rolls the transaction back.
	WithTimer(ctx context.Context, fn func(TimerTx) error) error

	// PutTimer stores data at the given timer key with an explicit due time.
	// Overwriting an existing timer held under a live lease returns ErrTimerLeased.
	PutTimer(ctx context.Context, timerKey string, dueAt time.Time, data []byte) error

	// GetTimer retrieves the data stored at the given timer key.
	GetTimer(ctx context.Context, timerKey string) ([]byte, error)

	// DeleteTimer deletes the timer record at the given key. It is unfenced and
	// intended for admin/cleanup; pollers should prefer DeleteTimerOwned.
	DeleteTimer(ctx context.Context, timerKey string) error

	// DeleteTimerOwned deletes the timer record only when it exists and is owned
	// by owner. Owner is an opaque per-claim fencing token and must be non-empty;
	// an empty owner returns ErrTimerOwnerEmpty. It returns (false, nil) when the
	// timer is missing or owned by a different owner.
	DeleteTimerOwned(ctx context.Context, timerKey string, owner string) (bool, error)

	// ListTimers returns all timer keys matching the given prefix.
	ListTimers(ctx context.Context, prefix string) ([]string, error)

	// ClaimTimer atomically claims a timer lease. It claims only when the timer
	// exists, is due at or before now, and is unowned or its lease is expired.
	// Owner is an opaque per-claim fencing token and must be non-empty; an empty
	// owner returns ErrTimerOwnerEmpty. It returns (false, nil) for a missing
	// timer, a not-yet-due timer, or a timer held by a live lease.
	ClaimTimer(ctx context.Context, timerKey string, owner string, lease time.Duration) (bool, error)

	// RenewTimer extends the lease only when the record exists, is owned by owner,
	// and the lease is still live (not expired). Owner must be non-empty; an empty
	// owner returns ErrTimerOwnerEmpty. It returns (false, nil) otherwise.
	RenewTimer(ctx context.Context, timerKey string, owner string, lease time.Duration) (bool, error)

	// DueTimers returns at most limit due, unleased timers ordered by due time
	// then key.
	DueTimers(ctx context.Context, now time.Time, limit int) ([]Timer, error)
}

// Config selects and configures a storage backend.
type Config struct {
	// Driver names the registered backend. Empty selects DefaultDriver.
	Driver string
	// DataDir is the backend's root directory (legacy).
	DataDir string
	// Path is the SQLite database file path or root location depending on backend.
	Path string
	// DSN is the PostgreSQL connection string. It must not be logged verbatim.
	DSN string
}
