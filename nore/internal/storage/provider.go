package storage

import (
	"context"
	"errors"
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

	// ErrTransactionActive reports a plain Store operation attempted while a
	// WithTx callback holds the store's transaction. Use the callback's Tx so
	// the operation participates in the same atomic unit.
	ErrTransactionActive = errors.New("storage: transaction already active")

	// ErrUnknownDriver reports a storage driver that no backend registered.
	ErrUnknownDriver = errors.New("storage: unknown driver")

	// ErrInvalidKey reports a key segment that cannot be represented safely.
	ErrInvalidKey = errors.New("storage: invalid key")

	// ErrClosed reports an operation on a store that has been closed.
	ErrClosed = errors.New("storage: closed")
)

// KV is the key/value surface shared by a Store and a transaction. Keys are
// opaque strings; callers construct them with Key or Prefix.
type KV interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Put(ctx context.Context, key string, data []byte) error
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) ([]string, error)
}

// Tx is a KV bound to one transaction. Every operation on a Tx participates in
// the transaction opened by Store.WithTx.
type Tx interface {
	KV
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
	// the transaction back. Nested WithTx calls return ErrNestedTransaction;
	// plain Store operations during fn return ErrTransactionActive.
	WithTx(ctx context.Context, fn func(Tx) error) error

	// Close releases the backend. Close during an active transaction returns
	// ErrTransactionActive.
	Close() error
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
