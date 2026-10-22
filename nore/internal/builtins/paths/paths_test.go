package paths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRootsResolveAPathInsideTheProjectRoot(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatalf("create the nested directory: %v", err)
	}

	roots := NewRoots(root, nil)

	resolved, err := roots.Resolve(root, nested)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved != nested {
		t.Errorf("resolved = %q, want %q", resolved, nested)
	}
}

// A relative path is resolved against the base directory.
func TestRootsResolveARelativePathAgainstTheBase(t *testing.T) {
	root := t.TempDir()

	roots := NewRoots(root, nil)

	resolved, err := roots.Resolve(root, "file.txt")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved != filepath.Join(root, "file.txt") {
		t.Errorf("resolved = %q, want it inside the project root", resolved)
	}
}

// A path outside the permitted roots is refused.
func TestRootsRefuseAPathOutsideThePermittedRoots(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	roots := NewRoots(root, nil)

	_, err := roots.Resolve(root, outside)
	if err == nil {
		t.Fatal("Resolve accepted a path outside the permitted roots")
	}
	if !strings.Contains(err.Error(), "not reachable") {
		t.Errorf("error = %v, want it to state that the path is not reachable", err)
	}
}

// A path that climbs out of the permitted roots with ".." is refused: the
// path is cleaned before it is checked.
func TestRootsRefuseAPathThatClimbsOut(t *testing.T) {
	root := t.TempDir()

	roots := NewRoots(root, nil)

	_, err := roots.Resolve(root, filepath.Join("..", "elsewhere"))
	if err == nil {
		t.Fatal("Resolve accepted a path that climbs out of the permitted roots")
	}
}

// An extra root extends the reach.
func TestRootsAcceptAnExtraRoot(t *testing.T) {
	root := t.TempDir()
	extra := t.TempDir()

	roots := NewRoots(root, []string{extra})

	if _, err := roots.Resolve(root, extra); err != nil {
		t.Errorf("Resolve refused a path inside an extra root: %v", err)
	}
}

// A symbolic link inside a permitted root does not grant reach outside it.
func TestRootsRefuseALinkThatLeavesThePermittedRoots(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatalf("write the secret: %v", err)
	}

	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("the platform does not support symbolic links: %v", err)
	}

	roots := NewRoots(root, nil)

	if _, err := roots.Resolve(root, link); err == nil {
		t.Fatal("Resolve accepted a link that leaves the permitted roots")
	}
}

// The base is the project root when there is one.
func TestRootsUseTheProjectRootAsTheBase(t *testing.T) {
	root := t.TempDir()

	roots := NewRoots(root, nil)

	if roots.Base() != root {
		t.Errorf("Base = %q, want %q", roots.Base(), root)
	}
}

// Without a project, the working directory of the process is the root: that
// is the directory a directly invoked capability runtime was invoked from.
func TestRootsFallBackToTheWorkingDirectory(t *testing.T) {
	working, err := os.Getwd()
	if err != nil {
		t.Fatalf("read the working directory: %v", err)
	}

	roots := NewRoots("", nil)

	if roots.Base() != working {
		t.Errorf("Base = %q, want %q", roots.Base(), working)
	}
}

// A path that does not exist is returned as an absolute path: the caller
// reports the missing path, which is more useful than a refusal.
func TestRootsReturnAMissingPathAsAnAbsolutePath(t *testing.T) {
	root := t.TempDir()

	roots := NewRoots(root, nil)

	resolved, err := roots.Resolve(root, "missing.txt")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved != filepath.Join(root, "missing.txt") {
		t.Errorf("resolved = %q, want %q", resolved, filepath.Join(root, "missing.txt"))
	}
}

func TestRootsRefuseAnEmptyPath(t *testing.T) {
	roots := NewRoots(t.TempDir(), nil)

	if _, err := roots.Resolve(t.TempDir(), "  "); err == nil {
		t.Fatal("Resolve accepted an empty path")
	}
}
