package storage

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kuro/internal/domain"
)

func testPaths(t *testing.T) Paths {
	t.Helper()
	data := t.TempDir()
	return Paths{Data: data, Config: filepath.Join(data, "config"), Cache: filepath.Join(data, "cache")}
}

func TestRepositoryCreateListResolveAcrossRestart(t *testing.T) {
	paths := testPaths(t)
	repo := NewRepository(paths)
	entry := testEntry()
	if _, err := repo.Create(entry); err != nil {
		t.Fatalf("create failed: %v", err)
	}
	path := filepath.Join(paths.Data, "entries", "2026", "10", entry.ID+".md")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("entry file missing: %v", err)
	}
	restarted := NewRepository(paths)
	result, err := restarted.List()
	if err != nil || len(result.Issues) != 0 || len(result.Entries) != 1 {
		t.Fatalf("list after restart: entries=%d issues=%v err=%v", len(result.Entries), result.Issues, err)
	}
	resolved, _, err := restarted.Resolve(entry.ID[:8])
	if err != nil || resolved.ID != entry.ID {
		t.Fatalf("resolve failed: %#v %v", resolved, err)
	}
}

func TestRepositoryRejectsExistingTargetWithoutClobbering(t *testing.T) {
	paths := testPaths(t)
	repo := NewRepository(paths)
	entry := testEntry()
	if _, err := repo.Create(entry); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(paths.Data, "entries", "2026", "10", entry.ID+".md")
	original, _ := os.ReadFile(path)
	_, err := repo.Create(entry)
	if err == nil {
		t.Fatal("expected target_exists")
	}
	var resolveErr *ResolveError
	if !errors.As(err, &resolveErr) || resolveErr.Code != "target_exists" {
		t.Fatalf("got %T %v", err, err)
	}
	current, _ := os.ReadFile(path)
	if string(current) != string(original) {
		t.Fatal("existing file was changed")
	}
}

func TestRepositoryDuplicateRules(t *testing.T) {
	paths := testPaths(t)
	repo := NewRepository(paths)
	entry := testEntry()
	canonical := filepath.Join(paths.Data, "entries", "2026", "10", entry.ID+".md")
	copyPath := filepath.Join(paths.Data, "entries", "2026", "11", "copy.md")
	writeEntryFile(t, canonical, entry)
	writeEntryFile(t, copyPath, entry)
	result, err := repo.List()
	if err != nil || len(result.Entries) != 1 || len(result.Excluded) != 1 {
		t.Fatalf("canonical duplicate handling: %#v %v", result, err)
	}

	paths2 := testPaths(t)
	repo2 := NewRepository(paths2)
	first := filepath.Join(paths2.Data, "entries", "2026", "10", "copy-a.md")
	second := filepath.Join(paths2.Data, "entries", "2026", "11", "copy-b.md")
	writeEntryFile(t, first, entry)
	writeEntryFile(t, second, entry)
	result, err = repo2.List()
	if err != nil || len(result.Entries) != 0 || len(result.Excluded) != 2 {
		t.Fatalf("non-canonical duplicate handling: %#v %v", result, err)
	}
}

func TestRepositorySkipsHiddenAndSymlinkFiles(t *testing.T) {
	paths := testPaths(t)
	entry := testEntry()
	canonical := filepath.Join(paths.Data, "entries", "2026", "10", entry.ID+".md")
	writeEntryFile(t, canonical, entry)
	hidden := filepath.Join(paths.Data, "entries", "2026", "10", ".hidden.md")
	if err := os.WriteFile(hidden, []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(paths.Data, "entries", "2026", "10", "link.md")
	if err := os.Symlink(canonical, link); err != nil && !errors.Is(err, os.ErrPermission) {
		t.Fatal(err)
	}
	result, err := NewRepository(paths).List()
	if err != nil || len(result.Entries) != 1 {
		t.Fatalf("unexpected list: %#v %v", result, err)
	}
}

func TestResolveValidationAndAmbiguity(t *testing.T) {
	paths := testPaths(t)
	repo := NewRepository(paths)
	a := testEntry()
	b := a
	b.ID = "01JB7Q2M8X4T9K3V5N6P0R1S2B"
	b.CreatedAt = b.CreatedAt.Add(time.Minute)
	writeEntryFile(t, filepath.Join(paths.Data, "entries", "2026", "10", a.ID+".md"), a)
	writeEntryFile(t, filepath.Join(paths.Data, "entries", "2026", "10", b.ID+".md"), b)
	_, _, err := repo.Resolve("01JB7Q")
	var resolveErr *ResolveError
	if !errors.As(err, &resolveErr) || resolveErr.Code != "ambiguous_id" {
		t.Fatalf("expected ambiguity, got %T %v", err, err)
	}
	_, _, err = repo.Resolve("no!")
	if !errors.As(err, &resolveErr) || resolveErr.Code != "usage" {
		t.Fatalf("expected usage error, got %T %v", err, err)
	}
}

func writeEntryFile(t *testing.T, path string, entry domain.Entry) {
	t.Helper()
	data, err := Serialize(entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestParseOversizedFileIssue(t *testing.T) {
	paths := testPaths(t)
	path := filepath.Join(paths.Data, "entries", "2026", "10", "oversize.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", MaxFileSize+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := NewRepository(paths).List()
	if err != nil || len(result.Issues) != 1 || result.Issues[0].Code != "parse_error" {
		t.Fatalf("unexpected oversized result: %#v %v", result, err)
	}
}

func TestAtomicWriteInstallerIsNoOverwriteBoundary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "entry.md")
	original := []byte("original")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	previous := installFile
	installFile = func(_, _ string) error {
		return &ResolveError{Code: "target_exists", Message: "target already exists"}
	}
	t.Cleanup(func() { installFile = previous })
	if _, err := atomicWrite(path, []byte("replacement")); err == nil {
		t.Fatal("expected installer error")
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != string(original) {
		t.Fatal("existing file was overwritten")
	}
	assertNoTempFiles(t, dir)
}

func TestInstallNoReplaceClassifiesLinkErrors(t *testing.T) {
	previousLink, previousRename := linkFile, renameFile
	t.Cleanup(func() {
		linkFile, renameFile = previousLink, previousRename
	})
	calledRename := false
	renameOK := func(_, _ string) error {
		calledRename = true
		return nil
	}

	linkFile, renameFile = func(_, _ string) error { return errLinkUnsupported }, renameOK
	if err := installNoReplaceFile("temp", "final"); err != nil {
		t.Fatal(err)
	}
	if !calledRename {
		t.Fatal("unsupported hard links should use no-replace rename")
	}

	calledRename = false
	linkFile = func(_, _ string) error { return errTargetExists }
	assertInstallCode(t, installNoReplaceFile("temp", "final"), "target_exists")
	if calledRename {
		t.Fatal("target exists must not fall back")
	}

	linkFile = func(_, _ string) error { return errOtherInstall }
	if err := installNoReplaceFile("temp", "final"); err == nil {
		t.Fatal("unexpected link error should fail")
	}
	if calledRename {
		t.Fatal("unexpected link error must not fall back")
	}

	linkFile = func(_, _ string) error { return errLinkUnsupported }
	renameFile = func(_, _ string) error { return errTargetExists }
	assertInstallCode(t, installNoReplaceFile("temp", "final"), "target_exists")

	renameFile = func(_, _ string) error { return errRenameUnsupported }
	assertInstallCode(t, installNoReplaceFile("temp", "final"), "io_error")
}

func assertInstallCode(t *testing.T, err error, code string) {
	t.Helper()
	var resolveErr *ResolveError
	if !errors.As(err, &resolveErr) || resolveErr.Code != code {
		t.Fatalf("got %T %v, want code %q", err, err, code)
	}
}

func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".kuro-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary files left behind: %v", matches)
	}
}

func TestAtomicWriteTempCleanupFailureIsAWarning(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "entry.md")
	previous := removeFile
	removeFile = func(string) error { return errOtherInstall }
	t.Cleanup(func() { removeFile = previous })
	warnings, err := atomicWrite(path, []byte("saved"))
	if err != nil {
		t.Fatalf("an installed entry must not be reported as a failure: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("want one warning, got %v", warnings)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "saved" {
		t.Fatalf("entry not installed: %q %v", data, err)
	}
}

func TestCreateReportsSavedUnconfirmedWhenDirectorySyncFails(t *testing.T) {
	paths := testPaths(t)
	entry := testEntry()
	monthDir := filepath.Join(paths.Data, "entries", "2026", "10")
	previous := syncDir
	syncDir = func(dir string) error {
		if dir == monthDir {
			return errOtherInstall
		}
		return nil
	}
	t.Cleanup(func() { syncDir = previous })
	_, err := NewRepository(paths).Create(entry)
	var unconfirmed *SavedUnconfirmedError
	if !errors.As(err, &unconfirmed) {
		t.Fatalf("got %T %v, want SavedUnconfirmedError", err, err)
	}
	if unconfirmed.ID != entry.ID || unconfirmed.Path != "entries/2026/10/"+entry.ID+".md" {
		t.Fatalf("error does not identify the saved entry: %#v", unconfirmed)
	}
	if !strings.Contains(err.Error(), "do not add it again") {
		t.Fatalf("message should warn against retrying: %v", err)
	}
	if _, err := os.Stat(filepath.Join(monthDir, entry.ID+".md")); err != nil {
		t.Fatalf("entry file should exist: %v", err)
	}
}

func TestCreateSyncsParentsOfNewDirectories(t *testing.T) {
	paths := testPaths(t)
	var synced []string
	previous := syncDir
	syncDir = func(dir string) error {
		synced = append(synced, dir)
		return nil
	}
	t.Cleanup(func() { syncDir = previous })
	if _, err := NewRepository(paths).Create(testEntry()); err != nil {
		t.Fatal(err)
	}
	entries := filepath.Join(paths.Data, "entries")
	want := []string{paths.Data, entries, filepath.Join(entries, "2026"), filepath.Join(entries, "2026", "10")}
	if strings.Join(synced, "|") != strings.Join(want, "|") {
		t.Fatalf("synced %v, want %v", synced, want)
	}

	synced = nil
	second := testEntry()
	second.ID = "01JB7Q2M8X4T9K3V5N6P0R1S2B"
	if _, err := NewRepository(paths).Create(second); err != nil {
		t.Fatal(err)
	}
	if len(synced) != 1 || synced[0] != filepath.Join(entries, "2026", "10") {
		t.Fatalf("existing directories should only sync the month directory, got %v", synced)
	}
}

func TestResolveAmbiguousListsCandidates(t *testing.T) {
	paths := testPaths(t)
	repo := NewRepository(paths)
	for _, suffix := range []string{"A", "B", "C", "D", "E", "F"} {
		entry := testEntry()
		entry.ID = "01JB7Q2M8X4T9K3V5N6P0R1S2" + suffix
		if _, err := repo.Create(entry); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err := repo.Resolve("01JB7Q2M")
	var resolveErr *ResolveError
	if !errors.As(err, &resolveErr) || resolveErr.Code != "ambiguous_id" {
		t.Fatalf("expected ambiguity, got %T %v", err, err)
	}
	if len(resolveErr.Candidates) != 5 {
		t.Fatalf("want 5 candidates, got %v", resolveErr.Candidates)
	}
	for _, candidate := range resolveErr.Candidates {
		if !strings.Contains(err.Error(), candidate) {
			t.Fatalf("message %q does not list candidate %s", err.Error(), candidate)
		}
	}
	if !strings.Contains(err.Error(), "(and 1 more)") {
		t.Fatalf("message should mention the extra match: %q", err.Error())
	}
}
