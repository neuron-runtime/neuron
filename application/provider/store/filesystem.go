package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	providerapp "github.com/neuron-runtime/neuron/application/provider"
)

type FileSystemStore struct {
	root string
}

func NewFileSystemStore(root string) (*FileSystemStore, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("provider store root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve provider store root: %w", err)
	}
	return &FileSystemStore{root: filepath.Clean(abs)}, nil
}

func (s *FileSystemStore) Root() string { return s.root }

func (s *FileSystemStore) InstalledDir(name, version, digest string) (string, error) {
	return s.installedDir(name, version, digest)
}

func (s *FileSystemStore) Stage() (string, error) {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return "", fmt.Errorf("create provider store root: %w", err)
	}
	return os.MkdirTemp(s.root, ".tmp-*")
}

func (s *FileSystemStore) Commit(ctx context.Context, stage, name, version, digest string) (*providerapp.Installed, error) {
	final, err := s.installedDir(name, version, digest)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(stage); err != nil {
		return nil, fmt.Errorf("staging dir missing: %w", err)
	}
	if _, err := os.Stat(final); err == nil {
		return nil, fmt.Errorf("%w: %s@%s@%s", providerapp.ErrAlreadyInstalled, name, version, digest)
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return nil, fmt.Errorf("create provider parent directory: %w", err)
	}
	if err := os.Rename(stage, final); err != nil {
		return nil, fmt.Errorf("commit provider %s@%s@%s: %w", name, version, digest, err)
	}
	rec, err := providerapp.ReadInstallRecord(filepath.Join(final, providerapp.InstallFile))
	if err != nil {
		return nil, fmt.Errorf("read install record: %w", err)
	}
	m, err := providerapp.ReadManifest(filepath.Join(final, providerapp.ManifestFile))
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	return &providerapp.Installed{Name: rec.Provider, Version: rec.Version, Digest: rec.Digest, RootDir: final, ManifestPath: filepath.Join(final, providerapp.ManifestFile), Manifest: m}, nil
}

func (s *FileSystemStore) Get(ctx context.Context, name, version string) (*providerapp.Installed, error) {
	base, err := s.typeDir(name)
	if err != nil {
		return nil, err
	}
	versionDir := filepath.Join(base, strings.TrimSpace(version))
	entries, err := os.ReadDir(versionDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s@%s", providerapp.ErrNotFound, name, version)
		}
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		d := filepath.Join(versionDir, e.Name())
		recPath := filepath.Join(d, providerapp.InstallFile)
		mPath := filepath.Join(d, providerapp.ManifestFile)
		if _, err := os.Stat(recPath); err != nil {
			continue
		}
		rec, err := providerapp.ReadInstallRecord(recPath)
		if err != nil {
			continue
		}
		m, _ := providerapp.ReadManifest(mPath)
		return &providerapp.Installed{Name: rec.Provider, Version: rec.Version, Digest: rec.Digest, RootDir: d, ManifestPath: mPath, Manifest: m}, nil
	}
	return nil, fmt.Errorf("%w: %s@%s", providerapp.ErrNotFound, name, version)
}

func (s *FileSystemStore) List(ctx context.Context, name string) ([]providerapp.Installed, error) {
	var out []providerapp.Installed
	if strings.TrimSpace(name) == "" {
		for _, dir := range s.walkInstallDirs(s.root) {
			rec, err := providerapp.ReadInstallRecord(filepath.Join(dir, providerapp.InstallFile))
			if err != nil {
				continue
			}
			m, _ := providerapp.ReadManifest(filepath.Join(dir, providerapp.ManifestFile))
			out = append(out, providerapp.Installed{Name: rec.Provider, Version: rec.Version, Digest: rec.Digest, RootDir: dir, ManifestPath: filepath.Join(dir, providerapp.ManifestFile), Manifest: m})
		}
	} else {
		base, err := s.typeDir(name)
		if err != nil {
			return nil, err
		}
		for _, versionDir := range s.listVersionDirs(base) {
			entries, err := os.ReadDir(versionDir)
			if err != nil {
				continue
			}
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				d := filepath.Join(versionDir, e.Name())
				rec, err := providerapp.ReadInstallRecord(filepath.Join(d, providerapp.InstallFile))
				if err != nil {
					continue
				}
				m, _ := providerapp.ReadManifest(filepath.Join(d, providerapp.ManifestFile))
				out = append(out, providerapp.Installed{Name: rec.Provider, Version: rec.Version, Digest: rec.Digest, RootDir: d, ManifestPath: filepath.Join(d, providerapp.ManifestFile), Manifest: m})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version > out[j].Version })
	return out, nil
}

func (s *FileSystemStore) Remove(ctx context.Context, name, version string) error {
	base, err := s.typeDir(name)
	if err != nil {
		return err
	}
	versionDir := filepath.Join(base, strings.TrimSpace(version))
	entries, err := os.ReadDir(versionDir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s@%s", providerapp.ErrNotFound, name, version)
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			if err := os.RemoveAll(filepath.Join(versionDir, e.Name())); err != nil {
				return err
			}
		}
	}
	_ = os.Remove(versionDir)
	return nil
}

func (s *FileSystemStore) walkInstallDirs(root string) []string {
	var out []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() || info.Name() != providerapp.InstallFile {
			return nil
		}
		out = append(out, filepath.Dir(path))
		return nil
	})
	return out
}

func (s *FileSystemStore) typeDir(name string) (string, error) {
	path, err := providerapp.ProviderTypePath(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.root, path), nil
}

func (s *FileSystemStore) installedDir(name, version, digest string) (string, error) {
	base, err := s.typeDir(name)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(version) == "" {
		return "", fmt.Errorf("provider version is required")
	}
	if strings.TrimSpace(digest) == "" {
		return "", fmt.Errorf("provider digest is required")
	}
	return filepath.Join(base, strings.TrimSpace(version), strings.TrimSpace(digest)), nil
}

func (s *FileSystemStore) listVersionDirs(base string) []string {
	entries, err := os.ReadDir(base)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return nil
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, filepath.Join(base, e.Name()))
		}
	}
	return dirs
}
