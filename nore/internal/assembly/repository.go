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

var (
	// ErrArtifactMismatch reports that the artifact stored under a requested key
	// does not describe the identity that key names. Resolution never silently
	// falls back to a different artifact.
	ErrArtifactMismatch = errors.New("assembly: artifact identity mismatch")

	// ErrAmbiguousArtifact reports that a content hash without an environment
	// matches more than one stored artifact.
	ErrAmbiguousArtifact = errors.New("assembly: ambiguous artifact")
)

// Repository persists RegisteredAssembly artifacts under
// assemblies/{assemblyID}/{version}/{hash}/{env}/manifest keys in the shared
// storage.Store, plus a per-assembly version index (versions.json) that makes
// (assemblyID, version) the source of truth.
//
// Registering the same version again REPLACES the previous artifact(s) for that
// version: earlier hashes are pruned, so a given name:version always addresses
// exactly one artifact, the newest one. Artifacts are therefore not
// content-addressed immutable; that retention limitation remains until Phase 4.
type Repository struct {
	store storage.Store
}

func NewRepository(store storage.Store) *Repository {
	return &Repository{store: store}
}

// Register persists reg durably and makes (assemblyID, version) point at it.
// Registering the same version clears the previously recorded artifact(s) for
// that version, regardless of hash or environment, then writes the new one and
// sets it as the latest version. The artifact write, pruning, and index update
// happen in one transaction, so a failed registration leaves no partial state.
// The request is idempotent when the exact same artifact is already current:
// created=false, replaced=false.
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

	storageKey, err := r.key(reg.Key)
	if err != nil {
		return false, false, err
	}
	data := mustJSON(reg)

	err = r.store.WithTx(ctx, func(tx storage.Tx) error {
		idx, err := r.loadIndex(ctx, tx, reg.Key.AssemblyID)
		if err != nil {
			return err
		}
		old, existed := idx.Versions[reg.Key.Version]

		if err := tx.Put(ctx, storageKey, data); err != nil {
			return err
		}

		// Enforce a single artifact per version: clear every other artifact
		// previously recorded under assemblies/{id}/{version}/*.
		if err := r.pruneVersionArtifacts(ctx, tx, reg.Key); err != nil {
			return err
		}

		entry := VersionEntry{
			Hash:         reg.Key.Hash,
			Env:          reg.Key.Env,
			RegisteredAt: reg.RegisteredAt,
		}
		idx.Versions[reg.Key.Version] = entry
		idx.Latest = &VersionRef{Version: reg.Key.Version, VersionEntry: entry}
		if err := r.saveIndex(ctx, tx, reg.Key.AssemblyID, idx); err != nil {
			return err
		}

		if existed {
			created = false
			replaced = old.Hash != entry.Hash || old.Env != entry.Env
		} else {
			created = true
			replaced = false
		}
		return nil
	})
	if err != nil {
		return false, false, err
	}
	return created, replaced, nil
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
//   - When a hash is supplied it must be paired with a concrete version. The
//     hash addresses exactly that artifact: a key with an environment loads the
//     literal manifest key, while a key without one requires exactly one
//     artifact under the version/hash prefix. Missing artifacts return
//     storage.ErrNotFound, ambiguous ones return ErrAmbiguousArtifact, and
//     stored artifacts whose identity disagrees with the key return
//     ErrArtifactMismatch. Hash resolution never falls back to the index.
//   - Otherwise the version is resolved through the assembly's version index
//     (latest resolves to the most recently registered version) and the hash
//     and environment come from that index entry.
//   - An explicitly requested environment that is no longer current falls back
//     to scanning the version's artifacts so legacy multi-env data stays
//     addressable.
func (r *Repository) Resolve(ctx context.Context, key protocol.InstanceKey) (RegisteredAssembly, error) {
	if key.Hash != "" {
		return r.resolveByHash(ctx, key)
	}

	idx, err := r.loadIndex(ctx, r.store, key.AssemblyID)
	if err != nil {
		return RegisteredAssembly{}, err
	}
	version, entry, err := idx.Select(key.Version)
	if err != nil {
		return RegisteredAssembly{}, fmt.Errorf("registered assembly %s: %w", key.String(), err)
	}

	if key.Env != "" && key.Env != entry.Env {
		reg, scanErr := r.resolveInVersion(ctx, r.store, key.AssemblyID, version, key.Env)
		if scanErr != nil {
			if errors.Is(scanErr, storage.ErrNotFound) {
				return RegisteredAssembly{}, fmt.Errorf("registered assembly %s: env %q not registered", key.String(), key.Env)
			}
			return RegisteredAssembly{}, fmt.Errorf("registered assembly %s: %w", key.String(), scanErr)
		}
		return reg, nil
	}

	full := key
	full.Version = version
	full.Hash = entry.Hash
	full.Env = entry.Env

	storageKey, err := r.key(full)
	if err != nil {
		return RegisteredAssembly{}, err
	}
	reg, err := r.getByStorageKey(ctx, r.store, storageKey)
	if err != nil {
		return RegisteredAssembly{}, fmt.Errorf("registered assembly %s: %w", key.String(), err)
	}
	return reg, nil
}

// resolveByHash loads the exact artifact named by key.Hash. A content hash is
// only meaningful together with a concrete version. When an environment is
// supplied it addresses one literal key; otherwise the version/hash prefix must
// contain exactly one manifest.
func (r *Repository) resolveByHash(ctx context.Context, key protocol.InstanceKey) (RegisteredAssembly, error) {
	if key.Version == "" || key.Version == protocol.VersionLatest {
		return RegisteredAssembly{}, fmt.Errorf(
			"registered assembly %s: a content hash requires a concrete version, got %q",
			key.String(), key.Version,
		)
	}

	if key.Env != "" {
		storageKey, err := r.key(key)
		if err != nil {
			return RegisteredAssembly{}, err
		}
		reg, err := r.getByStorageKey(ctx, r.store, storageKey)
		if err != nil {
			return RegisteredAssembly{}, fmt.Errorf("registered assembly %s: %w", key.String(), err)
		}
		if err := verifyArtifact(key, reg.Key, true); err != nil {
			return RegisteredAssembly{}, err
		}
		return reg, nil
	}

	prefix, err := storage.Prefix("assemblies", key.AssemblyID, key.Version, key.Hash)
	if err != nil {
		return RegisteredAssembly{}, err
	}
	keys, err := r.store.List(ctx, prefix)
	if err != nil {
		return RegisteredAssembly{}, err
	}

	matches := make([]string, 0, 1)
	for _, storageKey := range keys {
		if strings.HasSuffix(storageKey, "/manifest") {
			matches = append(matches, storageKey)
		}
	}

	switch len(matches) {
	case 0:
		return RegisteredAssembly{}, fmt.Errorf("%w: registered assembly %s", storage.ErrNotFound, key.String())
	case 1:
		reg, err := r.getByStorageKey(ctx, r.store, matches[0])
		if err != nil {
			return RegisteredAssembly{}, fmt.Errorf("registered assembly %s: %w", key.String(), err)
		}
		if err := verifyArtifact(key, reg.Key, false); err != nil {
			return RegisteredAssembly{}, err
		}
		return reg, nil
	default:
		return RegisteredAssembly{}, fmt.Errorf(
			"%w: registered assembly %s matches %d artifacts; specify an environment",
			ErrAmbiguousArtifact, key.String(), len(matches),
		)
	}
}

// verifyArtifact checks that a stored artifact describes the requested key.
// The environment is only compared when the caller requested one.
func verifyArtifact(want, got protocol.InstanceKey, checkEnv bool) error {
	if got.AssemblyID != want.AssemblyID ||
		got.Version != want.Version ||
		got.Hash != want.Hash ||
		(checkEnv && got.Env != want.Env) {
		return fmt.Errorf("%w: requested %s, stored artifact is %s", ErrArtifactMismatch, want.String(), got.String())
	}
	return nil
}

// resolveInVersion scans the artifacts registered under
// assemblies/{id}/{version}/* and returns the newest one. When env is non-empty
// only artifacts of that environment are considered. Read or decode failures
// are returned rather than treated as absence.
func (r *Repository) resolveInVersion(ctx context.Context, kv storage.KV, assemblyID, version, env string) (RegisteredAssembly, error) {
	prefix, err := storage.Prefix("assemblies", assemblyID, version)
	if err != nil {
		return RegisteredAssembly{}, err
	}
	keys, err := kv.List(ctx, prefix)
	if err != nil {
		return RegisteredAssembly{}, err
	}

	var found RegisteredAssembly
	for _, storageKey := range keys {
		if !strings.HasSuffix(storageKey, "/manifest") {
			continue
		}
		candidate, err := r.getByStorageKey(ctx, kv, storageKey)
		if err != nil {
			return RegisteredAssembly{}, fmt.Errorf("load artifact %q: %w", storageKey, err)
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
// the version is empty every version of the assembly is removed. The artifact
// deletes and index update happen in one transaction.
func (r *Repository) Delete(ctx context.Context, key protocol.InstanceKey) error {
	return r.store.WithTx(ctx, func(tx storage.Tx) error {
		idx, err := r.loadIndex(ctx, tx, key.AssemblyID)
		if err != nil {
			return err
		}

		if key.Version == "" || key.Version == protocol.VersionLatest {
			if err := r.deleteVersionArtifacts(ctx, tx, key.AssemblyID, ""); err != nil {
				return err
			}
			indexKey, err := r.indexKey(key.AssemblyID)
			if err != nil {
				return err
			}
			return tx.Delete(ctx, indexKey)
		}

		if err := r.deleteVersionArtifacts(ctx, tx, key.AssemblyID, key.Version); err != nil {
			return err
		}

		delete(idx.Versions, key.Version)
		idx.Latest = recomputeLatest(idx)
		return r.saveIndex(ctx, tx, key.AssemblyID, idx)
	})
}

// List returns every registered artifact, one per indexing assembly/version/hash.
func (r *Repository) List(ctx context.Context) ([]RegisteredAssembly, error) {
	prefix, err := storage.Prefix("assemblies")
	if err != nil {
		return nil, err
	}
	keys, err := r.store.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	records := make([]RegisteredAssembly, 0, len(keys))
	for _, key := range keys {
		if !strings.HasSuffix(key, "/manifest") {
			continue
		}
		rec, err := r.getByStorageKey(ctx, r.store, key)
		if err != nil {
			return nil, fmt.Errorf("load artifact %q: %w", key, err)
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
func (r *Repository) pruneVersionArtifacts(ctx context.Context, kv storage.KV, new protocol.InstanceKey) error {
	prefix, err := storage.Prefix("assemblies", new.AssemblyID, new.Version)
	if err != nil {
		return err
	}
	newKey, err := r.key(new)
	if err != nil {
		return err
	}
	keys, err := kv.List(ctx, prefix)
	if err != nil {
		return err
	}
	for _, storageKey := range keys {
		if !strings.HasSuffix(storageKey, "/manifest") || storageKey == newKey {
			continue
		}
		if err := kv.Delete(ctx, storageKey); err != nil {
			return err
		}
	}
	return nil
}

// deleteVersionArtifacts removes every artifact under assemblies/{id}/{version}/*.
// An empty version deletes every artifact of the assembly (but never the index).
func (r *Repository) deleteVersionArtifacts(ctx context.Context, kv storage.KV, assemblyID, version string) error {
	var prefix string
	var err error
	if version != "" {
		prefix, err = storage.Prefix("assemblies", assemblyID, version)
	} else {
		prefix, err = storage.Prefix("assemblies", assemblyID)
	}
	if err != nil {
		return err
	}
	keys, err := kv.List(ctx, prefix)
	if err != nil {
		return err
	}
	for _, storageKey := range keys {
		if !strings.HasSuffix(storageKey, "/manifest") {
			continue
		}
		if err := kv.Delete(ctx, storageKey); err != nil {
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

func (r *Repository) key(key protocol.InstanceKey) (string, error) {
	return storage.Key("assemblies", key.AssemblyID, key.Version, key.Hash, key.Env, "manifest")
}

func (r *Repository) getByStorageKey(ctx context.Context, kv storage.KV, storageKey string) (RegisteredAssembly, error) {
	var reg RegisteredAssembly
	data, err := kv.Get(ctx, storageKey)
	if err != nil {
		return reg, err
	}
	if err := json.Unmarshal(data, &reg); err != nil {
		return reg, fmt.Errorf("unmarshal registered assembly %q: %w", storageKey, err)
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
