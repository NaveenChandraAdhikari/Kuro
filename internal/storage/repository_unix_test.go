//go:build !windows

package storage

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("permission checks do not apply to root")
	}
}

func TestListIssuesDoNotExposeDataDirectory(t *testing.T) {
	skipIfRoot(t)
	paths := testPaths(t)
	locked := filepath.Join(paths.Data, "entries", "2026", "11")
	if err := os.MkdirAll(locked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	result, err := NewRepository(paths).List()
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Issues) != 1 {
		t.Fatalf("want one issue for the unreadable directory, got %v", result.Issues)
	}
	issue := result.Issues[0]
	if strings.Contains(issue.Message, paths.Data) || issue.Path != "entries/2026/11" {
		t.Fatalf("issue exposes the data directory: %#v", issue)
	}
}

func TestCreateErrorsDoNotExposeDataDirectory(t *testing.T) {
	skipIfRoot(t)
	paths := testPaths(t)
	monthDir := filepath.Join(paths.Data, "entries", "2026", "10")
	if err := os.MkdirAll(monthDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(monthDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(monthDir, 0o700) })
	_, err := NewRepository(paths).Create(testEntry())
	if err == nil {
		t.Fatal("expected a write error in a read-only directory")
	}
	if strings.Contains(err.Error(), paths.Data) {
		t.Fatalf("error exposes the data directory: %v", err)
	}
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("the original cause must stay inspectable: %v", err)
	}
}
