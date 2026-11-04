# R4-2 Reference-Aware Pruning Analysis

## Current `pruneVersionArtifacts` Implementation (lines 372-396)

```go
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
```

**Current behavior**: Blindly deletes all artifacts under a version except the newly written one. Not reference-aware.

**Key comment from repository.go line 34**: 
> "Artifacts are therefore not content-addressed immutable; that retention limitation remains until Phase 4."

## How `Repository.Resolve` Works with Hash (lines 181-238)

When a hash is supplied with a version:

1. **With environment** (lines 189-202): Loads the literal manifest key directly
   ```go
   if key.Env != "" {
       storageKey, err := r.key(key)
       // ...
       reg, err := r.getByStorageKey(ctx, r.store, storageKey)
       // ...
   }
   ```

2. **Without environment** (lines 204-237): Lists all manifests under `version/hash/` prefix
   - If 0 matches: `storage.ErrNotFound`
   - If 1 match: Loads and verifies the artifact
   - If >1 matches: `ErrAmbiguousArtifact` (different environments)

The hash **never falls back to the index** - it always resolves directly from storage.

## Current Same-Version Pruning Behavior

When registering the same version again (`Register` method, lines 50-111):

1. The old artifact(s) for that version are **always pruned** regardless of hash or environment
2. The index (`versions.json`) is updated to point to the new hash/environment
3. **Result**: `(assemblyID, version)` always addresses exactly one artifact - the newest one
4. **Limitation**: Older artifacts are lost even if executions reference them

**Execution impact**: If an execution was created with `app:1.0.0#old-hash:dev`, and then `app:1.0.0#new-hash:dev` is registered, the execution's hash becomes invalid and resolution will fail with `ErrNotFound`.

## How Executions Reference Assembly Artifacts

**Execution struct** (execution.go lines 97-105):
```go
type Execution struct {
    AssemblyID  string
    Version     string
    Hash        string
    Env         string
    // ...
}
```

**Creation flow** (manager.go GetOrCreate, lines 145-194):
1. `reg, err := m.assemblies.Get(ctx, key)` - resolves the registered assembly
2. `execOpt, err := withCapabilityRuntimes(reg)` - decodes capability runtimes
3. `i, err := New(m.parent, string(id), canonical, &reg.Assembly, ...)` - creates instance
4. `i.Start()` - starts the instance

**Key point**: Executions capture the assembly identity (`AssemblyID`, `Version`, `Hash`, `Env`) at creation time and store it independently. After creation, executions are **decoupled** from the repository - they have their own stored identity and don't re-query the repository for assembly resolution.

**Reference tracking gap**: There is **no mechanism** to track which executions reference which artifact hashes. The repository has no visibility into execution references.

## What Changes Are Needed for Reference-Aware Pruning

### Option A: Track Referenced Hashes in Pruning

**Modify `pruneVersionArtifacts`** to accept a set of referenced hashes:

```go
func (r *Repository) pruneVersionArtifacts(ctx context.Context, kv storage.KV, new protocol.InstanceKey, referencedHashes map[string]bool) error
```

- Before deleting an old artifact, check if its hash is in `referencedHashes`
- Only prune hashes with `false` (not referenced)

**Update `Register`** to pass referenced hashes:
- Collect all execution hashes for this assembly/version before pruning
- Pass them to `pruneVersionArtifacts`

**Update execution creation** to register references:
- When an execution is created, notify the repository of its hash reference
- Could use a separate reference store or update the version index

### Option B: Add Reference Counts to Version Index

Modify `VersionIndex` to track reference counts:

```go
type VersionEntry struct {
    Hash         string    `json:"hash"`
    Env          string    `json:"env"`
    RegisteredAt time.Time `json:"registered_at"`
    RefCount     int       `json:"ref_count"`  // NEW
}
```

- Increment when an execution references this hash
- Decrement when an execution is removed
- Prune only when `RefCount == 0`

**Changes needed**:
1. `index.go` - Add `RefCount` to `VersionEntry`
2. `repository.go` - Use `RefCount` in `pruneVersionArtifacts`
3. `manager.go` - Update `RefCount` when creating/resolving executions

### Option C: Separate Reference Store

Add a dedicated storage key for tracking references:

```
assemblies/{assemblyID}/refs/{version}/{hash} -> count
```

- Increment on execution creation
- Decrement on execution removal
- Prune only when count reaches 0

**Changes needed**:
1. Add new storage key pattern
2. Update `Repository` to read/write reference counts
3. Update `Manager` to maintain reference counts

## Existing Tests for Pruning Behavior

**Test `TestSameVersionRebuildReplacesOldHash`** (repository_test.go lines 77-113):
- Registers `app:1.0.0#old-hash:dev`, then `app:1.0.0#new-hash:dev`
- Verifies old hash is gone via `Resolve`
- Verifies `latest` points to new hash
- **This tests the current (non-reference-aware) behavior**

**Test `TestDeleteRemovesVersionAndKeepsOthers`** (repository_test.go lines 115-161):
- Tests `Delete` removing a specific version
- Verifies other versions remain intact

**No existing tests** for reference-aware pruning or reference tracking.

## Dependency Graph: What Other Packages Need to Change

```
┌─────────────────────────────────────────────────────────────────┐
│          assembly Repository (repository.go)                    │
│  - Modify pruneVersionArtifacts                                 │
│  - Add reference tracking mechanism                             │
└─────────────────────────────────────┬───────────────────────────┘
                                      │
                      ┌───────────────────────┘
                      │
              ┌─────────────────────┐
              │  instance Manager   │
              │  (manager.go)       │
              │  - Track execution   │
              │    hash references   │
              │  - Update refcounts  │
              │  - on execution      │
              │    creation/removal  │
              └─────────────────────┘
                      │
                      │
              ┌─────────────────────┐
              │  execution Store    │
              │  (execution store)  │
              │  - May need to       │
              │    report hash usage │
              └─────────────────────┘
```

**Packages that need modification**:

1. **`nore/internal/assembly/repository.go`** - Core pruning logic
2. **`nore/internal/instance/manager.go`** - Reference tracking on execution lifecycle
3. **`nore/internal/execution/execution.go`** - Execution already stores hash (no change needed)
4. **`nore/internal/index/version_index.go`** (if using Option B) - Reference counts in index

**Minimal change approach (Option A)**:
- Only modify `repository.go` and `manager.go`
- No changes to execution store or version index schema

**Full reference-aware approach (Option B)**:
- Modify `index.go` to add `RefCount`
- Modify `repository.go` to check refcounts
- Modify `manager.go` to maintain refcounts