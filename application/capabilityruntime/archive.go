package capabilityruntime

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// IsArchive reports whether path is a gzip-compressed tarball.
func IsArchive(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	// gzip magic: 1f 8b
	head := make([]byte, 2)
	if _, err := io.ReadFull(f, head); err != nil {
		return false
	}
	return head[0] == 0x1f && head[1] == 0x8b
}

// ExtractTarGz extracts a .tar.gz archive into dst. When every entry shares a
// single top-level directory (the common release layout: <name>-v1.2.0/...)
// that directory is stripped so the payload lands directly in dst. Path
// traversal outside dst is rejected.
func ExtractTarGz(archivePath, dst string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)

	// The shared prefix is discovered lazily: the first entry's top-level
	// directory is treated as the common root and stripped from every entry
	// that carries it.
	var prefix string

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		clean := filepath.ToSlash(hdr.Name)
		top := topLevel(clean)

		if prefix == "" && top != "" {
			prefix = top + "/"
		}

		rel := clean
		if after, ok := strings.CutPrefix(rel, prefix); ok {
			rel = after
		}
		if rel == "" {
			continue
		}

		target := filepath.Join(dst, filepath.FromSlash(rel))
		if !inside(dst, target) {
			return ErrManifestInvalid
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(out, tr)
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		default:
			// ignore hardlinks and other entry types.
		}
	}
	return nil
}

func topLevel(name string) string {
	name = filepath.ToSlash(name)
	trimmed := strings.Trim(name, "/")
	if trimmed == "" {
		return ""
	}
	if idx := strings.Index(trimmed, "/"); idx >= 0 {
		return trimmed[:idx]
	}
	return trimmed
}

func inside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
