package assembly

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

// indexFileName is the suffix of the per-assembly version index key. Its length
// is part of the storage contract: it must not collide with /manifest keys.
const indexFileName = "versions.json"

// VersionEntry is the per-version record a assembly's version index keeps.
// It points at the current artifact (hash + env) for that version and the time
// it was registered.
type VersionEntry struct {
	Hash         string    `json:"hash"`
	Env          string    `json:"env"`
	RegisteredAt time.Time `json:"registered_at"`
}

// VersionRef names the version a VersionEntry belongs to. It is solely used for
// the index's latest pointer.
type VersionRef struct {
	Version string `json:"version"`
	VersionEntry
}

// VersionIndex makes (assemblyID, version) the source of truth for addressing a
// registered artifact. Each version maps to exactly one current artifact;
// registering the same version again replaces the previous one. Latest points
// to the most recently registered version (time-based).
type VersionIndex struct {
	Latest   *VersionRef             `json:"latest,omitempty"`
	Versions map[string]VersionEntry `json:"versions"`
}

// newVersionIndex returns an initialized index, safe for mutation.
func newVersionIndex() *VersionIndex {
	return &VersionIndex{Versions: make(map[string]VersionEntry)}
}

// Select resolves a requested version to its concrete artifact. An empty or
// "latest" version resolves through the time-based latest pointer.
func (idx *VersionIndex) Select(version string) (string, VersionEntry, error) {
	if idx == nil {
		idx = newVersionIndex()
	}
	if version == "" || version == protocol.VersionLatest {
		if idx.Latest == nil {
			return "", VersionEntry{}, fmt.Errorf("%w: no registered versions", storage.ErrNotFound)
		}
		return idx.Latest.Version, idx.Latest.VersionEntry, nil
	}
	entry, ok := idx.Versions[version]
	if !ok {
		return "", VersionEntry{}, fmt.Errorf("%w: no registered version %q", storage.ErrNotFound, version)
	}
	return version, entry, nil
}

// indexKey is the storage key of a assembly's version index.
// It lives at assemblies/{assemblyID}/versions.json, outside the artifact tree.
func (r *Repository) indexKey(assemblyID string) (string, error) {
	return storage.Key("assemblies", assemblyID, indexFileName)
}

// loadIndex reads the assembly's version index through kv. When absent it is
// rebuilt from the registered artifacts and persisted, so pre-index data stays
// addressable.
func (r *Repository) loadIndex(ctx context.Context, kv storage.KV, assemblyID string) (*VersionIndex, error) {
	indexKey, err := r.indexKey(assemblyID)
	if err != nil {
		return nil, err
	}
	data, err := kv.Get(ctx, indexKey)
	if err == nil {
		var idx VersionIndex
		if err := json.Unmarshal(data, &idx); err != nil {
			return nil, fmt.Errorf("unmarshal version index for %s: %w", assemblyID, err)
		}
		return &idx, nil
	}
	if !errors.Is(err, storage.ErrNotFound) {
		return nil, err
	}
	return r.buildIndex(ctx, kv, assemblyID)
}

// buildIndex scans every artifact of a assembly and reconstructs its version
// index. Rebuilding is self-healing: it only runs when no index exists yet.
func (r *Repository) buildIndex(ctx context.Context, kv storage.KV, assemblyID string) (*VersionIndex, error) {
	prefix, err := storage.Prefix("assemblies", assemblyID)
	if err != nil {
		return nil, err
	}
	keys, err := kv.List(ctx, prefix)
	if err != nil {
		return nil, err
	}

	idx := newVersionIndex()
	for _, storageKey := range keys {
		if !strings.HasSuffix(storageKey, "/manifest") {
			continue
		}
		reg, err := r.getByStorageKey(ctx, kv, storageKey)
		if err != nil {
			return nil, fmt.Errorf("rebuild index for %s: load artifact %q: %w", assemblyID, storageKey, err)
		}
		entry := VersionEntry{
			Hash:         reg.Key.Hash,
			Env:          reg.Key.Env,
			RegisteredAt: reg.RegisteredAt,
		}
		if prev, ok := idx.Versions[reg.Key.Version]; !ok || newerEntry(reg.Key.Version, entry, reg.Key.Version, prev) {
			idx.Versions[reg.Key.Version] = entry
		}
		if idx.Latest == nil || newerEntry(reg.Key.Version, entry, idx.Latest.Version, idx.Latest.VersionEntry) {
			idx.Latest = &VersionRef{Version: reg.Key.Version, VersionEntry: entry}
		}
	}

	if err := r.saveIndex(ctx, kv, assemblyID, idx); err != nil {
		return nil, err
	}
	return idx, nil
}

func (r *Repository) saveIndex(ctx context.Context, kv storage.KV, assemblyID string, idx *VersionIndex) error {
	if idx == nil {
		idx = newVersionIndex()
	}
	data, err := json.Marshal(idx)
	if err != nil {
		return fmt.Errorf("marshal version index for %s: %w", assemblyID, err)
	}
	indexKey, err := r.indexKey(assemblyID)
	if err != nil {
		return err
	}
	return kv.Put(ctx, indexKey, data)
}

// newerEntry reports whether (verA, entryA) is newer than (verB, entryB).
// Newer means registered later in time; ties break on version string then hash
// so resolution stays deterministic.
func newerEntry(verA string, a VersionEntry, verB string, b VersionEntry) bool {
	if !a.RegisteredAt.Equal(b.RegisteredAt) {
		return a.RegisteredAt.After(b.RegisteredAt)
	}
	if verA != verB {
		return verA > verB
	}
	return a.Hash > b.Hash
}
