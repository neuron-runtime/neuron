package utils

import (
	"fmt"
	"os"
	"path/filepath"
)

// ResolveRoot normalizes the honored project root: flag first, cwd fallback.
func ResolveRoot(flag string) (string, error) {
	root := flag
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("get current directory: %w", err)
		}
		root = cwd
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve project root %q: %w", root, err)
	}
	return abs, nil
}
