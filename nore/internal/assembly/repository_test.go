package assembly

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/nore/internal/storage/sqlite"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

func newTestRepository(t *testing.T) (*Repository, storage.Store) {
	t.Helper()

	store, err := sqlite.New(context.Background(), storage.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	return NewRepository(store), store
}

func testRegistration(assemblyID, version, hash, env string) RegisteredAssembly {
	return RegisteredAssembly{
		Key: protocol.InstanceKey{
			AssemblyID: assemblyID,
			Version:    version,
			Hash:       hash,
			Env:        env,
		},
	}
}

func register(t *testing.T, repo *Repository, assemblyID, version, hash, env string) {
	t.Helper()

	if _, _, err := repo.Register(context.Background(), testRegistration(assemblyID, version, hash, env)); err != nil {
		t.Fatalf("register %s:%s#%s:%s: %v", assemblyID, version, hash, env, err)
	}
}

func TestResolveByExactHash(t *testing.T) {
	ctx := context.Background()
	repo, _ := newTestRepository(t)
	register(t, repo, "app", "1.0.0", "hash-one", "dev")

	got, err := repo.Resolve(ctx, protocol.InstanceKey{
		AssemblyID: "app",
		Version:    "1.0.0",
		Hash:       "hash-one",
		Env:        "dev",
	})
	if err != nil {
		t.Fatalf("resolve hash with env: %v", err)
	}
	if got.Key.Hash != "hash-one" || got.Key.Env != "dev" {
		t.Fatalf("resolved unexpected artifact: %+v", got.Key)
	}

	got, err = repo.Resolve(ctx, protocol.InstanceKey{
		AssemblyID: "app",
		Version:    "1.0.0",
		Hash:       "hash-one",
	})
	if err != nil {
		t.Fatalf("resolve hash without env: %v", err)
	}
	if got.Key.Hash != "hash-one" {
		t.Fatalf("resolved unexpected hash: %+v", got.Key)
	}
}

func TestSameVersionRebuildReplacesOldHash(t *testing.T) {
	ctx := context.Background()
	repo, _ := newTestRepository(t)
	register(t, repo, "app", "1.0.0", "old-hash", "dev")
	register(t, repo, "app", "1.0.0", "new-hash", "dev")

	_, err := repo.Resolve(ctx, protocol.InstanceKey{
		AssemblyID: "app",
		Version:    "1.0.0",
		Hash:       "old-hash",
		Env:        "dev",
	})
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("old hash should be gone, got %v", err)
	}

	got, err := repo.Resolve(ctx, protocol.InstanceKey{
		AssemblyID: "app",
		Version:    "1.0.0",
		Hash:       "new-hash",
		Env:        "dev",
	})
	if err != nil {
		t.Fatalf("resolve new hash: %v", err)
	}
	if got.Key.Hash != "new-hash" {
		t.Fatalf("resolved unexpected hash: %+v", got.Key)
	}

	latest, err := repo.Resolve(ctx, protocol.InstanceKey{AssemblyID: "app"})
	if err != nil {
		t.Fatalf("resolve latest: %v", err)
	}
	if latest.Key.Hash != "new-hash" {
		t.Fatalf("latest should point at new hash, got %+v", latest.Key)
	}
}

func TestDeleteRemovesVersionAndKeepsOthers(t *testing.T) {
	ctx := context.Background()
	repo, _ := newTestRepository(t)
	register(t, repo, "app", "1.0.0", "hash-v1", "dev")
	register(t, repo, "app", "2.0.0", "hash-v2", "dev")

	if err := repo.Delete(ctx, protocol.InstanceKey{AssemblyID: "app", Version: "1.0.0"}); err != nil {
		t.Fatalf("delete version: %v", err)
	}

	_, err := repo.Resolve(ctx, protocol.InstanceKey{
		AssemblyID: "app",
		Version:    "1.0.0",
		Hash:       "hash-v1",
		Env:        "dev",
	})
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("deleted version hash should be gone, got %v", err)
	}

	got, err := repo.Resolve(ctx, protocol.InstanceKey{
		AssemblyID: "app",
		Version:    "2.0.0",
		Hash:       "hash-v2",
		Env:        "dev",
	})
	if err != nil {
		t.Fatalf("other version should remain: %v", err)
	}
	if got.Key.Version != "2.0.0" {
		t.Fatalf("resolved unexpected version: %+v", got.Key)
	}

	// A key with no version removes every version of the assembly.
	if err := repo.Delete(ctx, protocol.InstanceKey{AssemblyID: "app"}); err != nil {
		t.Fatalf("delete all versions: %v", err)
	}
	_, err = repo.Resolve(ctx, protocol.InstanceKey{
		AssemblyID: "app",
		Version:    "2.0.0",
		Hash:       "hash-v2",
		Env:        "dev",
	})
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("force-deleted hash should be gone, got %v", err)
	}
}

func TestDistinctVersionsResolveIndependently(t *testing.T) {
	ctx := context.Background()
	repo, _ := newTestRepository(t)
	register(t, repo, "app", "1.0.0", "hash-v1", "dev")
	register(t, repo, "app", "2.0.0", "hash-v2", "prod")

	for _, key := range []protocol.InstanceKey{
		{AssemblyID: "app", Version: "1.0.0", Hash: "hash-v1", Env: "dev"},
		{AssemblyID: "app", Version: "2.0.0", Hash: "hash-v2", Env: "prod"},
	} {
		got, err := repo.Resolve(ctx, key)
		if err != nil {
			t.Fatalf("resolve %s: %v", key.String(), err)
		}
		if got.Key != key {
			t.Fatalf("resolve %s returned %s", key.String(), got.Key.String())
		}
	}

	for _, key := range []protocol.InstanceKey{
		{AssemblyID: "app", Version: "1.0.0", Hash: "hash-v1"},
		{AssemblyID: "app", Version: "2.0.0", Hash: "hash-v2"},
	} {
		got, err := repo.Resolve(ctx, key)
		if err != nil {
			t.Fatalf("resolve %s: %v", key.String(), err)
		}
		if got.Key.AssemblyID != key.AssemblyID || got.Key.Version != key.Version || got.Key.Hash != key.Hash {
			t.Fatalf("resolve %s returned %s", key.String(), got.Key.String())
		}
	}
}

func TestHashRequiresConcreteVersion(t *testing.T) {
	ctx := context.Background()
	repo, _ := newTestRepository(t)
	register(t, repo, "app", "1.0.0", "hash-v1", "dev")

	for _, version := range []string{"", protocol.VersionLatest} {
		_, err := repo.Resolve(ctx, protocol.InstanceKey{
			AssemblyID: "app",
			Version:    version,
			Hash:       "hash-v1",
			Env:        "dev",
		})
		if err == nil {
			t.Fatalf("hash with version %q should fail", version)
		}
		if !strings.Contains(err.Error(), "requires a concrete version") {
			t.Fatalf("unexpected error for version %q: %v", version, err)
		}
	}
}

func TestHashAbsentAndMismatchedArtifacts(t *testing.T) {
	ctx := context.Background()
	repo, store := newTestRepository(t)
	register(t, repo, "app", "1.0.0", "hash-v1", "dev")

	for _, key := range []protocol.InstanceKey{
		{AssemblyID: "app", Version: "1.0.0", Hash: "missing", Env: "dev"},
		{AssemblyID: "app", Version: "1.0.0", Hash: "missing"},
	} {
		_, err := repo.Resolve(ctx, key)
		if !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("absent hash %s should be ErrNotFound, got %v", key.String(), err)
		}
	}

	// Store a manifest under a key whose embedded identity disagrees with it.
	mismatched := testRegistration("app", "1.0.0", "embedded-hash", "dev")
	key, err := storage.Key("assemblies", "app", "1.0.0", "hash-v2", "dev", "manifest")
	if err != nil {
		t.Fatalf("build storage key: %v", err)
	}
	if err := store.Put(ctx, key, mustJSON(mismatched)); err != nil {
		t.Fatalf("write mismatched artifact: %v", err)
	}

	_, err = repo.Resolve(ctx, protocol.InstanceKey{
		AssemblyID: "app",
		Version:    "1.0.0",
		Hash:       "hash-v2",
		Env:        "dev",
	})
	if !errors.Is(err, ErrArtifactMismatch) {
		t.Fatalf("mismatched artifact should be ErrArtifactMismatch, got %v", err)
	}
}

func TestAmbiguousHashWithoutEnv(t *testing.T) {
	ctx := context.Background()
	repo, store := newTestRepository(t)

	for _, env := range []string{"dev", "prod"} {
		reg := testRegistration("app", "1.0.0", "hash-v1", env)
		key, err := storage.Key("assemblies", "app", "1.0.0", "hash-v1", env, "manifest")
		if err != nil {
			t.Fatalf("build storage key: %v", err)
		}
		if err := store.Put(ctx, key, mustJSON(reg)); err != nil {
			t.Fatalf("write %s artifact: %v", env, err)
		}
	}

	_, err := repo.Resolve(ctx, protocol.InstanceKey{
		AssemblyID: "app",
		Version:    "1.0.0",
		Hash:       "hash-v1",
	})
	if !errors.Is(err, ErrAmbiguousArtifact) {
		t.Fatalf("two env artifacts should be ambiguous, got %v", err)
	}
}

// failingIndexStore wraps a real store and injects a failure when the
// transaction writes the version index. The artifact write before it must roll
// back.
type failingIndexStore struct {
	storage.Store
	failSuffix string
}

func (s *failingIndexStore) WithTx(ctx context.Context, fn func(storage.Tx) error) error {
	return s.Store.WithTx(ctx, func(tx storage.Tx) error {
		return fn(failingIndexTx{Tx: tx, failSuffix: s.failSuffix})
	})
}

type failingIndexTx struct {
	storage.Tx
	failSuffix string
}

func (tx failingIndexTx) Put(ctx context.Context, key string, data []byte) error {
	if strings.HasSuffix(key, tx.failSuffix) {
		return errors.New("injected index write failure")
	}
	return tx.Tx.Put(ctx, key, data)
}

func TestRegisterIsAtomicWhenIndexWriteFails(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.New(ctx, storage.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Seed an empty index so Register reaches the artifact write before the
	// injected index failure.
	baseRepo := NewRepository(store)
	indexKey, err := baseRepo.indexKey("app")
	if err != nil {
		t.Fatalf("build index key: %v", err)
	}
	data, err := json.Marshal(newVersionIndex())
	if err != nil {
		t.Fatalf("marshal index: %v", err)
	}
	if err := store.Put(ctx, indexKey, data); err != nil {
		t.Fatalf("seed index: %v", err)
	}

	failingRepo := NewRepository(&failingIndexStore{Store: store, failSuffix: indexFileName})
	_, _, err = failingRepo.Register(ctx, testRegistration("app", "1.0.0", "hash-v1", "dev"))
	if err == nil {
		t.Fatal("registration should fail when the index write fails")
	}

	_, err = baseRepo.Resolve(ctx, protocol.InstanceKey{
		AssemblyID: "app",
		Version:    "1.0.0",
		Hash:       "hash-v1",
		Env:        "dev",
	})
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("artifact should have rolled back, got %v", err)
	}
}
