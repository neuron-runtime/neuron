package assembly

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/nore/internal/storage/sqlite"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

// Repository persists RegisteredAssembly artifacts under
// assemblies/{assemblyID}/{version}/{hash}/{env}/manifest keys in the shared
// storage.Store, plus a per-assembly version index (versions.json) that makes
// (assemblyID, version) the source of truth.
//
// Each content hash is an immutable artifact; registering the same version
// again REPLACES the previous artifact for that version, so a given
// name:version always addresses exactly one artifact — the newest one.
type Repository struct {
	store storage.Store
}

func NewRepository(store storage.Store) *Repository {
	return &Repository{store: store}
}

// Register persists reg durably and makes (assemblyID, version) point at it.
// Registering the same version clears the previously recorded artifact(s) for
// that version, regardless of hash or environment, then writes the new one and
// sets it as the latest version. The request is idempotent when the exact same
// artifact is already current: created=false, replaced=false.
func (r *Repository) Register(ctx context.Context, reg RegisteredAssembly) (created bool, replaced bool, err error) {
	if reg.Key.AssemblyID == "" {
		return false, false, fmt.Errorf("register requires a assembly id")
	}
	if reg.Key.Version == "" {
		reg.Key.Version = protocol.VersionLatest
	}
	if reg.RegisteredAt.IsZero() {
		reg.RegisteredAt = time.Now().UTC()
	}
	if reg.UpdatedAt.IsZero() {
		reg.UpdatedAt = reg.RegisteredAt
	}

	idx, err := r.loadIndex(ctx, reg.Key.AssemblyID)
	if err != nil {
		return false, false, err
	}
	old, existed := idx.Versions[reg.Key.Version]

	storageKey := r.key(reg.Key)
	if err := r.store.Put(ctx, storageKey, mustJSON(reg)); err != nil {
		return false, false, err
	}

	// Enforce a single artifact per version: clear every other artifact
	// previously recorded under assemblies/{id}/{version}/*.
	if err := r.pruneVersionArtifacts(ctx, reg.Key); err != nil {
		return false, false, err
	}

	entry := VersionEntry{
		Hash:         reg.Key.Hash,
		Env:          reg.Key.Env,
		RegisteredAt: reg.RegisteredAt,
	}
	idx.Versions[reg.Key.Version] = entry
	idx.Latest = &VersionRef{Version: reg.Key.Version, VersionEntry: entry}
	if err := r.saveIndex(ctx, reg.Key.AssemblyID, idx); err != nil {
		return false, false, err
	}

	if existed {
		if old.Hash == entry.Hash && old.Env == entry.Env {
			return false, false, nil
		}
		return false, true, nil
	}
	return true, false, nil
}

// Get resolves the durable artifact for key. See Resolve for how partial keys
// are normalized.
func (r *Repository) Get(ctx context.Context, key protocol.InstanceKey) (RegisteredAssembly, error) {
	return r.Resolve(ctx, key)
}

// Resolve returns the artifact a possibly-partial key addresses. Accepted
// forms are assemblyID, assemblyID:latest, assemblyID:version, and
// assemblyID:version:hash, each optionally suffixed with :env.
//
//   - A fully-specified hash maps directly to its immutable artifact.
//   - Otherwise the version is resolved through the assembly's version index
//     (latest resolves to the most recently registered version) and the hash
//     and environment come from that index entry.
//   - An explicitly requested environment that is no longer current falls back
//     to scanning the version's artifacts so legacy multi-env data stays
//     addressable.
func (r *Repository) Resolve(ctx context.Context, key protocol.InstanceKey) (RegisteredAssembly, error) {
	if key.Hash != "" {
		reg, err := r.resolveInVersion(ctx, key.AssemblyID, key.Version, key.Env)
		if err != nil {
			return RegisteredAssembly{}, err
		}
		return reg, nil
	}

	idx, err := r.loadIndex(ctx, key.AssemblyID)
	if err != nil {
		return RegisteredAssembly{}, err
	}
	version, entry, err := idx.Select(key.Version)
	if err != nil {
		return RegisteredAssembly{}, fmt.Errorf("registered assembly %s: %w", key.String(), err)
	}

	if key.Env != "" && key.Env != entry.Env {
		reg, scanErr := r.resolveInVersion(ctx, key.AssemblyID, version, key.Env)
		if scanErr != nil {
			return RegisteredAssembly{}, fmt.Errorf("registered assembly %s: env %q not registered", key.String(), key.Env)
		}
		return reg, nil
	}

	full := key
	full.Version = version
	full.Hash = entry.Hash
	full.Env = entry.Env

	reg, err := r.getByStorageKey(ctx, r.key(full))
	if err != nil {
		return RegisteredAssembly{}, fmt.Errorf("registered assembly %s: %w", key.String(), err)
	}
	return reg, nil
}

// resolveInVersion scans the artifacts registered under
// assemblies/{id}/{version}/* and returns the newest one. When env is non-empty
// only artifacts of that environment are considered.
func (r *Repository) resolveInVersion(ctx context.Context, assemblyID, version, env string) (RegisteredAssembly, error) {
	prefix := sqlite.SanitizeKey("assemblies", assemblyID, version)
	keys, err := r.store.List(ctx, prefix+"/")
	if err != nil {
		return RegisteredAssembly{}, err
	}

	var found RegisteredAssembly
	for _, storageKey := range keys {
		if !strings.HasSuffix(storageKey, "/manifest") {
			continue
		}
		candidate, err := r.getByStorageKey(ctx, storageKey)
		if err != nil {
			continue
		}
		if env != "" && candidate.Key.Env != env {
			continue
		}
		if (found.Key == protocol.InstanceKey{}) || newerEntry(candidate.Key.Version, VersionEntry{
			Hash:         candidate.Key.Hash,
			Env:          candidate.Key.Env,
			RegisteredAt: candidate.RegisteredAt,
		}, found.Key.Version, VersionEntry{
			Hash:         found.Key.Hash,
			Env:          found.Key.Env,
			RegisteredAt: found.RegisteredAt,
		}) {
			found = candidate
		}
	}

	if (found.Key == protocol.InstanceKey{}) {
		return RegisteredAssembly{}, fmt.Errorf(
			"%w: registered assembly %s", storage.ErrNotFound, InstanceKeyString(assemblyID, version, env),
		)
	}
	return found, nil
}

func (r *Repository) Exists(ctx context.Context, key protocol.InstanceKey) (bool, error) {
	_, err := r.Resolve(ctx, key)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, storage.ErrNotFound) {
		return false, nil
	}
	return false, err
}

// Delete removes version's artifact(s) and its version index entry, then
// recomputes the latest pointer. A key may be partial (assemblyID:version); when
// the version is empty every version of the assembly is removed.
func (r *Repository) Delete(ctx context.Context, key protocol.InstanceKey) error {
	idx, err := r.loadIndex(ctx, key.AssemblyID)
	if err != nil {
		return err
	}

	if key.Version == "" || key.Version == protocol.VersionLatest {
		if err := r.deleteVersionArtifacts(ctx, key.AssemblyID, ""); err != nil {
			return err
		}
		return r.store.Delete(ctx, r.indexKey(key.AssemblyID))
	}

	if err := r.deleteVersionArtifacts(ctx, key.AssemblyID, key.Version); err != nil {
		return err
	}

	delete(idx.Versions, key.Version)
	idx.Latest = recomputeLatest(idx)
	return r.saveIndex(ctx, key.AssemblyID, idx)
}

// List returns every registered artifact, one per indexing assembly/version/hash.
func (r *Repository) List(ctx context.Context) ([]RegisteredAssembly, error) {
	keys, err := r.store.List(ctx, "assemblies/")
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	records := make([]RegisteredAssembly, 0, len(keys))
	for _, key := range keys {
		if !strings.HasSuffix(key, "/manifest") {
			continue
		}
		rec, err := r.getByStorageKey(ctx, key)
		if err != nil {
			continue
		}
		identity := rec.Key.AssemblyID + "\x00" + rec.Key.Version + "\x00" + rec.Key.Hash + "\x00" + rec.Key.Env
		if seen[identity] {
			continue
		}
		seen[identity] = true
		records = append(records, rec)
	}
	return records, nil
}

// pruneVersionArtifacts deletes every artifact under assemblies/{id}/{version}/*
// except the just-written one for new.
func (r *Repository) pruneVersionArtifacts(ctx context.Context, new protocol.InstanceKey) error {
	prefix := sqlite.SanitizeKey("assemblies", new.AssemblyID, new.Version)
	keys, err := r.store.List(ctx, prefix+"/")
	if err != nil {
		return err
	}
	for _, storageKey := range keys {
		if !strings.HasSuffix(storageKey, "/manifest") || storageKey == r.key(new) {
			continue
		}
		if err := r.store.Delete(ctx, storageKey); err != nil {
			return err
		}
	}
	return nil
}

// deleteVersionArtifacts removes every artifact under assemblies/{id}/{version}/*.
// An empty version deletes every artifact of the assembly (but never the index).
func (r *Repository) deleteVersionArtifacts(ctx context.Context, assemblyID, version string) error {
	prefix := sqlite.SanitizeKey("assemblies", assemblyID)
	if version != "" {
		prefix += "/" + version
	}
	keys, err := r.store.List(ctx, prefix+"/")
	if err != nil {
		return err
	}
	for _, storageKey := range keys {
		if !strings.HasSuffix(storageKey, "/manifest") {
			continue
		}
		if err := r.store.Delete(ctx, storageKey); err != nil {
			return err
		}
	}
	return nil
}

// recomputeLatest rebuilds the index's latest pointer from its versions.
func recomputeLatest(idx *VersionIndex) *VersionRef {
	if idx == nil {
		return nil
	}
	var latest *VersionRef
	for version, entry := range idx.Versions {
		if latest == nil || newerEntry(version, entry, latest.Version, latest.VersionEntry) {
			latest = &VersionRef{Version: version, VersionEntry: entry}
		}
	}
	return latest
}

func (r *Repository) key(key protocol.InstanceKey) string {
	return sqlite.SanitizeKey("assemblies", key.AssemblyID, key.Version, key.Hash, key.Env, "manifest")
}

func (r *Repository) getByStorageKey(ctx context.Context, storageKey string) (RegisteredAssembly, error) {
	var reg RegisteredAssembly
	data, err := r.store.Get(ctx, storageKey)
	if err != nil {
		return reg, err
	}
	if err := json.Unmarshal(data, &reg); err != nil {
		return reg, err
	}
	return reg, nil
}

func mustJSON(reg RegisteredAssembly) []byte {
	data, err := json.Marshal(reg)
	if err != nil {
		panic(fmt.Sprintf("marshal registered assembly: %v", err))
	}
	return data
}

// InstanceKeyString renders a possibly-partial key for error messages.
func InstanceKeyString(assemblyID, version, env string) string {
	s := assemblyID
	if version != "" {
		s += ":" + version
	}
	if env != "" {
		s += ":" + env
	}
	return s
}
