package sqlite_test

import (
	"context"
	"errors"
	"testing"

	"github.com/neuron-runtime/neuron/nore/internal/storage"
	_ "github.com/neuron-runtime/neuron/nore/internal/storage/sqlite"
)

func TestOpenSelectsRegisteredDriver(t *testing.T) {
	for _, driver := range []string{"sqlite", "SQLite", ""} {
		t.Run("driver="+driver, func(t *testing.T) {
			store, err := storage.Open(context.Background(), storage.Config{
				Driver:  driver,
				DataDir: t.TempDir(),
			})
			if err != nil {
				t.Fatalf("open driver %q: %v", driver, err)
			}
			defer store.Close()
			if err := store.Ping(context.Background()); err != nil {
				t.Fatalf("ping opened store: %v", err)
			}
		})
	}
}

func TestOpenRejectsUnknownDriver(t *testing.T) {
	_, err := storage.Open(context.Background(), storage.Config{
		Driver:  "does-not-exist",
		DataDir: t.TempDir(),
	})
	if !errors.Is(err, storage.ErrUnknownDriver) {
		t.Fatalf("open unknown driver = %v, want ErrUnknownDriver", err)
	}
}
