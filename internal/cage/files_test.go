package cage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsurePrivateFileRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("create symlink: %v", err)
	}

	err := ensurePrivateFile(link, "test file")
	if err == nil {
		t.Fatal("ensurePrivateFile accepted a symlink")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error = %q, want symlink error", err)
	}
}

func TestAtomicWriteFileSetsModeOnCreateAndOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := atomicWriteFile(path, []byte("one")); err != nil {
		t.Fatal(err)
	}
	assertMode(t, path, 0o600)

	makeInsecurePermissions(t, path, 0o644)
	if err := atomicWriteFile(path, []byte("two")); err != nil {
		t.Fatal(err)
	}
	assertMode(t, path, 0o600)
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "two" {
		t.Fatalf("data = %q, want two", string(data))
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode = %v, want %v", got, want)
	}
}
