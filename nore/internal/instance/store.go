package instance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/storage"
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
	key, err := ms.key(rec.ID)
	if err != nil {
		return err
	}
	return ms.store.Put(ctx, key, data)
}

// Get reports storage.ErrNotFound for absence. A backend or decode failure is
// returned and must not be interpreted as a missing instance.
func (ms *metadataStore) Get(ctx context.Context, id string) (metadata, error) {
	key, err := ms.key(id)
	if err != nil {
		return metadata{}, err
	}
	data, err := ms.store.Get(ctx, key)
	if err != nil {
		return metadata{}, err
	}
	var rec metadata
	if err := json.Unmarshal(data, &rec); err != nil {
		return metadata{}, fmt.Errorf("decode instance metadata %q: %w", id, err)
	}
	return rec, nil
}

func (ms *metadataStore) List(ctx context.Context) ([]metadata, error) {
	prefix, err := storage.Prefix("instances")
	if err != nil {
		return nil, err
	}
	keys, err := ms.store.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("list instance metadata: %w", err)
	}
	records := make([]metadata, 0, len(keys))
	for _, key := range keys {
		id, err := instanceIDFromKey(key)
		if err != nil {
			return nil, err
		}
		rec, err := ms.Get(ctx, id)
		if errors.Is(err, storage.ErrNotFound) {
			// The record was deleted between List and Get.
			continue
		}
		if err != nil {
			return nil, err
		}
		records = append(records, rec)
	}
	return records, nil
}

func (ms *metadataStore) Delete(ctx context.Context, id string) error {
	key, err := ms.key(id)
	if err != nil {
		return err
	}
	return ms.store.Delete(ctx, key)
}

func (ms *metadataStore) key(id string) (string, error) {
	return storage.Key("instances", id)
}

func instanceIDFromKey(key string) (string, error) {
	prefix, err := storage.Prefix("instances")
	if err != nil {
		return "", err
	}
	if len(key) <= len(prefix) || key[:len(prefix)] != prefix {
		return "", fmt.Errorf("decode instance key %q: unexpected prefix", key)
	}
	return key[len(prefix):], nil
}
