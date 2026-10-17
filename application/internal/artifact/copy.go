package artifact

import (
	"os"
	"path/filepath"
)

// CopyDir recursively copies the directory tree rooted at src into dst,
// creating directories as needed. Regular files keep their executable bit
// (mode 0755 when any execute bit is set, otherwise 0644); all other
// metadata is deliberately not preserved, so a staged copy depends only on
// the source bytes and names.
func CopyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := os.MkdirAll(dstPath, 0o755); err != nil {
				return err
			}
			if err := CopyDir(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if entry.Type()&0o111 != 0 {
			mode = 0o755
		}
		if err := os.WriteFile(dstPath, data, mode); err != nil {
			return err
		}
	}
	return nil
}
