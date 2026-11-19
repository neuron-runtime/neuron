package storage_test

import (
	"testing"

	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/nore/internal/storage/storagetest"
)

func TestMemoryStoreContract(t *testing.T) {
	open := func(t *testing.T) storage.Store {
		s := storage.NewMemoryStore()
		t.Cleanup(func() { _ = s.Close() })
		return s
	}
	storagetest.Run(t, open)
	storagetest.RunTimerContract(t, open)
}
