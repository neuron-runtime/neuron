// Package paths resolves the paths the built-in capability runtimes may reach.
//
// The built-ins that touch the filesystem — the filesystem built-in and the
// command built-in — must agree on what is reachable, so the rule lives here
// rather than in each of them.
//
// The rule is containment: a path is reachable when it resolves inside one of
// the permitted roots. The check is made on the resolved path, after symbolic
// links are followed, because a link inside a root is a way out of it.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Roots is the set of directories a built-in capability runtime may reach.
type Roots struct {
	// roots are the permitted directories as they were configured, made
	// absolute.
	roots []string

	// resolved are the permitted directories with their symbolic links
	// followed, where that was possible. A target is checked against both
	// lists, because a path that reaches a root through a link is still
	// inside it.
	resolved []string
}

// NewRoots builds the permitted roots.
//
// The project root is the directory the assembly lives in, and it is always
// permitted. When there is no project — a capability runtime invoked
// directly — the working directory of the process is the root, because that
// is the directory the operator invoked it from.
//
// The extra roots extend the reach to the directories the assembly is
// expected to work with.
func NewRoots(projectRoot string, extra []string) Roots {
	roots := make([]string, 0, len(extra)+1)

	if strings.TrimSpace(projectRoot) != "" {
		roots = append(roots, absolute(projectRoot))
	} else if working, err := os.Getwd(); err == nil {
		roots = append(roots, absolute(working))
	}

	for _, root := range extra {
		if strings.TrimSpace(root) == "" {
			continue
		}
		roots = append(roots, absolute(root))
	}

	resolved := make([]string, 0, len(roots))
	for _, root := range roots {
		if followed, err := filepath.EvalSymlinks(root); err == nil {
			resolved = append(resolved, filepath.Clean(followed))
		}
	}

	return Roots{roots: roots, resolved: resolved}
}

// Base returns the directory a relative path is resolved against.
//
// It is the project root when there is one, and the working directory of the
// process otherwise.
func (r Roots) Base() string {
	if len(r.roots) == 0 {
		return ""
	}
	return r.roots[0]
}

// Roots returns the permitted roots, for an error message that says what is
// reachable.
func (r Roots) Roots() []string {
	roots := make([]string, len(r.roots))
	copy(roots, r.roots)
	return roots
}

// Resolve resolves target against base and returns it when it is reachable.
//
// A relative target is resolved against base. The resolved path is checked
// twice: once as it was written, and once through the symbolic links it
// contains. A target that leaves the permitted roots through a link is
// refused even though the link itself is inside them.
//
// A target that does not exist is returned as an absolute path: the caller
// reports the missing path, which is a more useful error than a refusal.
func (r Roots) Resolve(base, target string) (string, error) {
	if strings.TrimSpace(target) == "" {
		return "", fmt.Errorf("the path is empty")
	}

	absolute := target
	if !filepath.IsAbs(absolute) {
		if base == "" {
			base = "."
		}
		absolute = filepath.Join(base, absolute)
	}
	absolute = filepath.Clean(absolute)

	if !r.contains(absolute) {
		return "", r.refuse(absolute)
	}

	followed, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return absolute, nil
	}
	followed = filepath.Clean(followed)

	if !r.contains(followed) {
		return "", r.refuse(followed)
	}

	return followed, nil
}

// Contains reports whether the path is reachable.
func (r Roots) Contains(path string) bool {
	if !filepath.IsAbs(path) {
		path = filepath.Clean(filepath.Join(r.Base(), path))
	}
	return r.contains(filepath.Clean(path))
}

// contains reports whether the path is inside one of the permitted roots.
func (r Roots) contains(path string) bool {
	for _, root := range r.roots {
		if within(root, path) {
			return true
		}
	}
	for _, root := range r.resolved {
		if within(root, path) {
			return true
		}
	}
	return false
}

// refuse reports a path that is not reachable, and names the roots that are.
func (r Roots) refuse(path string) error {
	if len(r.roots) == 0 {
		return fmt.Errorf("the path %q is not reachable: no directory is permitted", path)
	}
	return fmt.Errorf("the path %q is not reachable: it is outside %s", path, strings.Join(r.roots, ", "))
}

// within reports whether path is root or is inside it.
func within(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	if relative == "." {
		return true
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// absolute makes a path absolute and clean.
func absolute(path string) string {
	if resolved, err := filepath.Abs(path); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(path)
}
