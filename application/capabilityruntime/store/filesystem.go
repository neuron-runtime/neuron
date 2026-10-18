package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/neuron-runtime/neuron/application/capabilityruntime"
	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
)

// FileassemblyStore is a Store rooted at a local directory, by default
// ~/.neuron/capabilityRuntimes.
type FileassemblyStore struct {
	root string
}

// NewFileassemblyStore validates and returns a fileassembly store rooted at root.
func NewFileassemblyStore(root string) (*FileassemblyStore, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("capability runtime store root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve capability runtime store root: %w", err)
	}
	return &FileassemblyStore{root: filepath.Clean(abs)}, nil
}

func (s *FileassemblyStore) Root() string {
	return s.root
}

// InstalledDir returns the final installed directory for an exact
// (type, version, digest). The directory may not exist yet.
func (s *FileassemblyStore) InstalledDir(typ, version, digest string) (string, error) {
	return s.installedDir(typ, version, digest)
}

func (s *FileassemblyStore) Stage() (string, error) {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return "", fmt.Errorf("create capability runtime store root: %w", err)
	}
	return os.MkdirTemp(s.root, ".tmp-*")
}

// Commit atomically renames a fully-populated staging directory into its
// final content-addressed location. The staging directory must already
// contain a complete install.json with final paths; the committed directory
// is never rewritten in place.
func (s *FileassemblyStore) Commit(ctx context.Context, stage, typ, version, digest string) (*capabilityruntime.Installed, error) {
	final, err := s.installedDir(typ, version, digest)
	if err != nil {
		return nil, err
	}

	if _, err := os.Stat(stage); err != nil {
		return nil, fmt.Errorf("staging dir missing: %w", err)
	}
	if _, err := os.Stat(final); err == nil {
		return nil, fmt.Errorf("%w: %s@%s@%s", capabilityruntime.ErrAlreadyInstalled, typ, version, digest)
	}

	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return nil, fmt.Errorf("create capability runtime parent directory: %w", err)
	}

	if err := os.Rename(stage, final); err != nil {
		return nil, fmt.Errorf("commit capability runtime %s@%s@%s: %w", typ, version, digest, err)
	}

	// Read the install record from the committed directory. The record was
	// written with final paths before the rename, so no rewrite is needed.
	rec, err := capabilityruntime.ReadInstallRecord(filepath.Join(final, capabilityrt.InstallFile))
	if err != nil {
		return nil, fmt.Errorf("read install record: %w", err)
	}
	return rec.Installed(), nil
}

// Get returns the installed capability runtime for an exact version. The
// store layout is content-addressed (<type>/<version>/<digest>/), so Get
// scans the version directory for any committed install.json. A stale
// flat-layout install found under the version directory is migrated before
// the scan; if it cannot be migrated and no content-addressed copy exists,
// the migration error is surfaced instead of a misleading not-found.
func (s *FileassemblyStore) Get(ctx context.Context, typ, version string) (*capabilityruntime.Installed, error) {
	base, err := s.typeDir(typ)
	if err != nil {
		return nil, err
	}
	versionDir := filepath.Join(base, strings.TrimSpace(version))
	_, migrateErr := migrateFlatInstall(versionDir)
	entries, err := os.ReadDir(versionDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s@%s", capabilityruntime.ErrNotFound, typ, version)
		}
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		recPath := filepath.Join(versionDir, e.Name(), capabilityrt.InstallFile)
		if _, err := os.Stat(recPath); err != nil {
			continue
		}
		rec, err := capabilityruntime.ReadInstallRecord(recPath)
		if err != nil {
			continue
		}
		return rec.Installed(), nil
	}
	if migrateErr != nil {
		return nil, migrateErr
	}
	return nil, fmt.Errorf("%w: %s@%s", capabilityruntime.ErrNotFound, typ, version)
}

func (s *FileassemblyStore) List(ctx context.Context, typ string) ([]capabilityruntime.Installed, error) {
	var out []capabilityruntime.Installed

	if strings.TrimSpace(typ) == "" {
		// List everything: find every install.json under the store root.
		// Stale flat-layout records are migrated in place first; a record that
		// cannot be migrated is skipped rather than failing the whole
		// inventory, matching the lenient scan below.
		seen := make(map[string]bool)
		for _, dir := range s.walkInstallDirs(s.root) {
			finalDir, err := migrateFlatInstall(dir)
			if err != nil || seen[finalDir] {
				continue
			}
			seen[finalDir] = true
			rec, err := capabilityruntime.ReadInstallRecord(filepath.Join(finalDir, capabilityrt.InstallFile))
			if err != nil {
				continue
			}
			rec.Installed().RootDir = finalDir
			out = append(out, *rec.Installed())
		}
	} else {
		base, err := s.typeDir(typ)
		if err != nil {
			return nil, err
		}
		for _, versionDir := range s.listVersionDirs(base) {
			// Migrate a stale flat install first so typed listings and the
			// content-addressed scan see the same layout. An unmigratable
			// version is skipped: it is unusable either way, and an exact
			// lookup surfaces the precise migration error from Get.
			if _, err := migrateFlatInstall(versionDir); err != nil {
				continue
			}
			entries, err := os.ReadDir(versionDir)
			if err != nil {
				continue
			}
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				recPath := filepath.Join(versionDir, e.Name(), capabilityrt.InstallFile)
				rec, err := capabilityruntime.ReadInstallRecord(recPath)
				if err != nil {
					continue
				}
				rec.Installed().RootDir = filepath.Join(versionDir, e.Name())
				out = append(out, *rec.Installed())
			}
		}
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].Version > out[j].Version
	})
	return out, nil
}

// walkInstallDirs returns every directory below root that directly contains an
// install.json record, flattening the <root>/<owner>/.../<version>/<digest> tree.
func (s *FileassemblyStore) walkInstallDirs(root string) []string {
	var out []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() || info.Name() != capabilityrt.InstallFile {
			return nil
		}
		out = append(out, filepath.Dir(path))
		return nil
	})
	return out
}

// Remove deletes every committed digest directory for an exact version,
// making removal a cache/garbage-collection operation rather than an update.
// Stale flat-layout files (pre-content-addressed installs) are deleted along
// with the digest directories so removal always empties the version.
func (s *FileassemblyStore) Remove(ctx context.Context, typ, version string) error {
	base, err := s.typeDir(typ)
	if err != nil {
		return err
	}
	versionDir := filepath.Join(base, strings.TrimSpace(version))
	entries, err := os.ReadDir(versionDir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s@%s", capabilityruntime.ErrNotFound, typ, version)
		}
		return err
	}
	for _, e := range entries {
		target := filepath.Join(versionDir, e.Name())
		if e.IsDir() {
			if err := os.RemoveAll(target); err != nil {
				return err
			}
			continue
		}
		if err := os.Remove(target); err != nil {
			return err
		}
	}
	// Remove the version directory itself if it is now empty.
	_ = os.Remove(versionDir)
	return nil
}

// installedDir returns the final content-addressed directory for an exact
// (type, version, digest).
func (s *FileassemblyStore) installedDir(typ, version, digest string) (string, error) {
	base, err := s.typeDir(typ)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(version) == "" {
		return "", fmt.Errorf("capability runtime version is required")
	}
	if strings.TrimSpace(digest) == "" {
		return "", fmt.Errorf("capability runtime digest is required")
	}
	return filepath.Join(base, strings.TrimSpace(version), strings.TrimSpace(digest)), nil
}

// typeDir maps a logical capability runtime name to its store branch:
// "github:read" -> <root>/github/read.
func (s *FileassemblyStore) typeDir(typ string) (string, error) {
	path, err := capabilityrt.TypePath(typ)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.root, path), nil
}

func (s *FileassemblyStore) listVersionDirs(base string) []string {
	entries, err := os.ReadDir(base)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return nil
	}
	var dirs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dirs = append(dirs, filepath.Join(base, e.Name()))
	}
	return dirs
}
