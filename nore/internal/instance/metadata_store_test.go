package instance

import (
	"context"
	"errors"
	"testing"

	"github.com/neuron-runtime/neuron/nore/internal/storage"
)

var errMetadata = errors.New("injected metadata failure")

type metadataFaultStore struct {
	get  func(string) ([]byte, error)
	list func(string) ([]string, error)
}

func (f *metadataFaultStore) Get(_ context.Context, key string) ([]byte, error) {
	if f.get != nil {
		return f.get(key)
	}
	return nil, storage.ErrNotFound
}

func (f *metadataFaultStore) Put(context.Context, string, []byte) error { return nil }
func (f *metadataFaultStore) Delete(context.Context, string) error      { return nil }

func (f *metadataFaultStore) List(_ context.Context, prefix string) ([]string, error) {
	if f.list != nil {
		return f.list(prefix)
	}
	return nil, nil
}

func (f *metadataFaultStore) WithTx(ctx context.Context, fn func(storage.Tx) error) error {
	return fn(f)
}

func (f *metadataFaultStore) Ping(context.Context) error { return nil }
func (f *metadataFaultStore) Close() error               { return nil }

func TestMetadataGetSurfacesBackendFailure(t *testing.T) {
	ms := newMetadataStore(&metadataFaultStore{get: func(string) ([]byte, error) { return nil, errMetadata }})
	if _, err := ms.Get(context.Background(), "inst_a"); !errors.Is(err, errMetadata) {
		t.Fatalf("Get error = %v, want injected failure", err)
	}
}

func TestMetadataGetReportsDecodeFailureAsError(t *testing.T) {
	ms := newMetadataStore(&metadataFaultStore{get: func(string) ([]byte, error) { return []byte("{not json"), nil }})
	if _, err := ms.Get(context.Background(), "inst_a"); err == nil || errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("Get malformed metadata = %v, want a decode error", err)
	}
}

func TestMetadataListSurfacesBackendFailure(t *testing.T) {
	ms := newMetadataStore(&metadataFaultStore{list: func(string) ([]string, error) { return nil, errMetadata }})
	if _, err := ms.List(context.Background()); !errors.Is(err, errMetadata) {
		t.Fatalf("List error = %v, want injected failure", err)
	}
}

func TestMetadataListReportsMalformedKey(t *testing.T) {
	ms := newMetadataStore(&metadataFaultStore{list: func(string) ([]string, error) {
		return []string{"other/inst_a"}, nil
	}})
	if _, err := ms.List(context.Background()); err == nil {
		t.Fatal("List with malformed key succeeded")
	}
}
