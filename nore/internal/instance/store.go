package instance

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/nore/internal/storage/sqlite"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

// metadata is the durable record persisted for an instance. Runtime objects
// (event bus, scheduler, engine) are deliberately not stored; they are
// reconstructed in memory from a Assembly when the instance is recreated.
type metadata struct {
	ID                string          `json:"id"`
	AssemblyID        string          `json:"assembly_id"`
	Version           string          `json:"version,omitempty"`
	Hash              string          `json:"hash,omitempty"`
	Env               string          `json:"env,omitempty"`
	Status            Status          `json:"status"`
	BlueprintMetadata shared.Metadata `json:"blueprint_metadata"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

// metadataStore persists instance metadata under instances/{instanceID} keys
// in the shared storage.Store.
type metadataStore struct {
	store storage.Store
}

func newMetadataStore(store storage.Store) *metadataStore {
	return &metadataStore{store: store}
}

func (ms *metadataStore) Save(ctx context.Context, rec metadata) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal instance metadata: %w", err)
	}
	return ms.store.Put(ctx, ms.key(rec.ID), data)
}

func (ms *metadataStore) Get(ctx context.Context, id string) (metadata, bool) {
	data, err := ms.store.Get(ctx, ms.key(id))
	if err != nil {
		return metadata{}, false
	}
	var rec metadata
	if err := json.Unmarshal(data, &rec); err != nil {
		return metadata{}, false
	}
	return rec, true
}

func (ms *metadataStore) List(ctx context.Context) ([]metadata, error) {
	keys, err := ms.store.List(ctx, "instances/")
	if err != nil {
		return nil, err
	}
	records := make([]metadata, 0, len(keys))
	for _, key := range keys {
		rec, ok := ms.Get(ctx, instanceIDFromKey(key))
		if !ok {
			continue
		}
		records = append(records, rec)
	}
	return records, nil
}

func (ms *metadataStore) Delete(ctx context.Context, id string) error {
	return ms.store.Delete(ctx, ms.key(id))
}

func (ms *metadataStore) key(id string) string {
	return sqlite.SanitizeKey("instances", id)
}

func instanceIDFromKey(key string) string {
	return key[len("instances/"):]
}
