package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Muhammad-Jay/neuron/application/capabilityruntime"
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

// DefaultStoreDir returns the default capability runtime store directory
// (~/.neuron/capabilityRuntimes).
func DefaultStoreDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".neuron/capabilityRuntimes"
	}
	return filepath.Join(home, ".neuron", "capabilityRuntimes")
}

func (s *FileassemblyStore) Stage() (string, error) {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return "", fmt.Errorf("create capability runtime store root: %w", err)
	}
	return os.MkdirTemp(s.root, ".tmp-*")
}

func (s *FileassemblyStore) Commit(ctx context.Context, stage, typ, version string) (*capabilityruntime.Installed, error) {
	rec, err := capabilityruntime.ReadInstallRecord(filepath.Join(stage, capabilityruntime.InstallFile))
	if err != nil {
		return nil, fmt.Errorf("installed record missing in staging dir: %w", err)
	}

	final, err := s.installedDir(typ, version)
	if err != nil {
		return nil, err
	}

	if _, err := os.Stat(stage); err != nil {
		return nil, fmt.Errorf("staging dir missing: %w", err)
	}
	if _, err := os.Stat(final); err == nil {
		return nil, fmt.Errorf("%w: %s@%s", capabilityruntime.ErrAlreadyInstalled, typ, version)
	}

	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return nil, fmt.Errorf("create capability runtime parent directory: %w", err)
	}

	if err := os.Rename(stage, final); err != nil {
		return nil, fmt.Errorf("commit capability runtime %s@%s: %w", typ, version, err)
	}

	// The install record was written against the staging directory. Rewrite it
	// in place with the final paths so later reads return a correct record.
	installed := rec.Installed()
	installed.RootDir = final
	installed.ManifestPath = filepath.Join(final, capabilityruntime.ManifestFile)
	installed.ArtifactPath = final

	if err := capabilityruntime.WriteInstallRecord(filepath.Join(final, capabilityruntime.InstallFile), capabilityruntime.RecordFor(installed)); err != nil {
		return nil, fmt.Errorf("rewrite install record: %w", err)
	}

	return installed, nil
}

func (s *FileassemblyStore) Get(ctx context.Context, typ, version string) (*capabilityruntime.Installed, error) {
	dir, err := s.installedDir(typ, version)
	if err != nil {
		return nil, err
	}
	recPath := filepath.Join(dir, capabilityruntime.InstallFile)
	if _, err := os.Stat(recPath); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s@%s", capabilityruntime.ErrNotFound, typ, version)
		}
		return nil, err
	}
	rec, err := capabilityruntime.ReadInstallRecord(recPath)
	if err != nil {
		return nil, err
	}
	return rec.Installed(), nil
}

func (s *FileassemblyStore) List(ctx context.Context, typ string) ([]capabilityruntime.Installed, error) {
	var out []capabilityruntime.Installed

	if strings.TrimSpace(typ) == "" {
		// List everything: find every install.json under the store root.
		for _, dir := range s.walkInstallDirs(s.root) {
			rec, err := capabilityruntime.ReadInstallRecord(filepath.Join(dir, capabilityruntime.InstallFile))
			if err != nil {
				continue
			}
			rec.Installed().RootDir = dir
			out = append(out, *rec.Installed())
		}
	} else {
		base, err := s.typeDir(typ)
		if err != nil {
			return nil, err
		}
		for _, versionDir := range s.listVersionDirs(base) {
			recPath := filepath.Join(versionDir, capabilityruntime.InstallFile)
			rec, err := capabilityruntime.ReadInstallRecord(recPath)
			if err != nil {
				continue
			}
			out = append(out, *rec.Installed())
		}
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].Version > out[j].Version
	})
	return out, nil
}

// walkInstallDirs returns every directory below root that directly contains an
// install.json record, flattening the <root>/owner/.../version tree.
func (s *FileassemblyStore) walkInstallDirs(root string) []string {
	var out []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() || info.Name() != capabilityruntime.InstallFile {
			return nil
		}
		out = append(out, filepath.Dir(path))
		return nil
	})
	return out
}

func (s *FileassemblyStore) Remove(ctx context.Context, typ, version string) error {
	dir, err := s.installedDir(typ, version)
	if err != nil {
		return err
	}
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s@%s", capabilityruntime.ErrNotFound, typ, version)
		}
		return err
	}
	return os.RemoveAll(dir)
}

// installedDir returns the final directory for an exact (type, version).
func (s *FileassemblyStore) installedDir(typ, version string) (string, error) {
	base, err := s.typeDir(typ)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(version) == "" {
		return "", fmt.Errorf("capability runtime version is required")
	}
	return filepath.Join(base, strings.TrimSpace(version)), nil
}

// typeDir maps a logical capability runtime name to its store branch:
// "github:read" -> <root>/github/read.
func (s *FileassemblyStore) typeDir(typ string) (string, error) {
	path, err := capabilityruntime.TypePath(typ)
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
