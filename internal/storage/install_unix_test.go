//go:build !windows

package storage

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// Platform error values used by the install classification tests.
var (
	errLinkUnsupported   error = syscall.ENOTSUP
	errTargetExists      error = syscall.EEXIST
	errOtherInstall      error = syscall.EACCES
	errRenameUnsupported error = syscall.EINVAL
)

func TestCreateUsesPrivatePermissions(t *testing.T) {
	paths := testPaths(t)
	entry := testEntry()
	if _, err := NewRepository(paths).Create(entry); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{
		filepath.Join(paths.Data, "entries"),
		filepath.Join(paths.Data, "entries", "2026"),
		filepath.Join(paths.Data, "entries", "2026", "10"),
	} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Fatalf("%s mode = %o, want 700", dir, info.Mode().Perm())
		}
	}
	info, err := os.Stat(filepath.Join(paths.Data, "entries", "2026", "10", entry.ID+".md"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("entry mode = %o, want 600", info.Mode().Perm())
	}
}
