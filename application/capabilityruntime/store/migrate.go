package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/neuron-runtime/neuron/application/capabilityruntime"
	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
)

// migrateFlatInstall ensures that the installed capability runtime directory at
// dir uses the content-addressed store layout (<version>/<digest>/) and
// returns the directory that now holds the install record.
//
// Installs committed before the store adopted content addressing leave their
// files — including install.json — directly under <version>/. Both the store
// readers and N.O.R.E. derive artifact paths from <version>/<digest>/, so a
// flat install is invisible and unusable until it is migrated here. The digest
// is authoritative: it is already recorded inside install.json, so migration
// only moves files into <version>/<digest>/ and rewrites the record paths.
//
// Migration is idempotent and convergent: a directory already named after its
// digest is returned unchanged, a partially completed migration is finished on
// the next read, and a completed digest copy found alongside stale flat files
// wins — the flat leftovers are deleted. On any failure an actionable error
// is returned; the caller decides whether to surface it or fall back to a
// lenient scan.
func migrateFlatInstall(dir string) (string, error) {
	rec, err := capabilityruntime.ReadInstallRecord(filepath.Join(dir, capabilityrt.InstallFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return dir, nil // nothing installed here (or already content-addressed)
		}
		return "", fmt.Errorf("stale capability runtime install at %s has an unreadable %s: %w; reinstall it with `neuron add --force`", dir, capabilityrt.InstallFile, err)
	}

	digest := strings.TrimSpace(rec.Digest)
	if digest == "" {
		return "", fmt.Errorf("stale capability runtime install %s@%s records no digest; reinstall it with `neuron add --force %s@%s`", rec.Type, rec.Version, rec.Type, rec.Version)
	}
	// The digest becomes a directory name, so it must be a single, ordinary
	// path segment; anything else is a corrupt record, not a store layout.
	if digest == "." || digest == ".." || digest != filepath.Base(digest) {
		return "", fmt.Errorf("stale capability runtime install %s@%s records an unusable digest %q; reinstall it with `neuron add --force %s@%s`", rec.Type, rec.Version, digest, rec.Type, rec.Version)
	}

	if filepath.Base(dir) == digest {
		return dir, nil // already content-addressed
	}

	target := filepath.Join(dir, digest)
	if _, err := os.Stat(filepath.Join(target, capabilityrt.InstallFile)); err == nil {
		// A complete content-addressed copy already exists (the artifact was
		// reinstalled after the layout migration while the stale flat files
		// were still present). The digest directory is authoritative, so drop
		// the flat leftovers that the store never reads.
		removeFlatLeftovers(dir)
		return target, nil
	}

	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", fmt.Errorf("create content-addressed directory for %s@%s: %w", rec.Type, rec.Version, err)
	}

	// Interrupted migrations may have left a partial rewritten record behind;
	// it is always safe to regenerate because it is derived from install.json.
	_ = os.Remove(filepath.Join(target, capabilityrt.InstallFile+".tmp"))

	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", dir, err)
	}
	for _, e := range entries {
		if e.Name() == capabilityrt.InstallFile || e.Name() == digest {
			continue
		}
		src := filepath.Join(dir, e.Name())
		dst := filepath.Join(target, e.Name())
		if err := movePath(src, dst); err != nil {
			return "", fmt.Errorf("migrate %s@%s: move %s: %w", rec.Type, rec.Version, e.Name(), err)
		}
	}

	// Rewrite the record after every other file has moved, and publish it as
	// the last step: a flat record still present means "migration incomplete",
	// so readers converge on the next attempt.
	migrated := *rec
	migrated.RootDir = target
	migrated.ManifestPath = relocate(rec.ManifestPath, dir, target)
	migrated.ArtifactPath = relocate(rec.ArtifactPath, dir, target)
	tmp := filepath.Join(target, capabilityrt.InstallFile+".tmp")
	if err := capabilityruntime.WriteInstallRecord(tmp, migrated); err != nil {
		return "", fmt.Errorf("rewrite install record for %s@%s: %w", rec.Type, rec.Version, err)
	}
	if err := os.Rename(tmp, filepath.Join(target, capabilityrt.InstallFile)); err != nil {
		return "", fmt.Errorf("publish install record for %s@%s: %w", rec.Type, rec.Version, err)
	}
	if err := os.Remove(filepath.Join(dir, capabilityrt.InstallFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("remove stale install record for %s@%s: %w", rec.Type, rec.Version, err)
	}
	return target, nil
}

// removeFlatLeftovers deletes stale flat-layout files directly under the
// version directory, keeping every subdirectory — under the content-addressed
// layout only digest directories may sit directly under <version>/. Cleanup is
// best effort: leftover files are never read, so a failure must not break
// later lookups.
func removeFlatLeftovers(versionDir string) {
	entries, err := os.ReadDir(versionDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		_ = os.Remove(filepath.Join(versionDir, e.Name()))
	}
}

// movePath moves src to dst, merging directories recursively. File renames
// overwrite the destination so a re-run converges after an interrupted
// migration, and a source that already vanished — a concurrent migration
// moved it — is accepted when the destination exists.
func movePath(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if _, statErr := os.Lstat(dst); statErr == nil {
				return nil
			}
		}
		return err
	}
	if !info.IsDir() {
		if err := os.Rename(src, dst); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				if _, statErr := os.Lstat(dst); statErr == nil {
					return nil
				}
			}
			return err
		}
		return nil
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := movePath(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			return err
		}
	}
	return os.Remove(src)
}

// relocate returns path rewritten to be rooted at newRoot when it was rooted
// at oldRoot; paths outside oldRoot are left untouched.
func relocate(path, oldRoot, newRoot string) string {
	if path == "" {
		return ""
	}
	if path == oldRoot {
		return newRoot
	}
	prefix := oldRoot + string(filepath.Separator)
	if strings.HasPrefix(path, prefix) {
		return filepath.Join(newRoot, strings.TrimPrefix(path, prefix))
	}
	return path
}
