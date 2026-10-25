package storage

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/oklog/ulid/v2"
	"kuro/internal/domain"
)

type Repository struct{ Paths Paths }

type Issue struct {
	Code    string   `json:"code"`
	Path    string   `json:"path"`
	Paths   []string `json:"paths,omitempty"`
	Line    int      `json:"line"`
	Message string   `json:"message"`
	Warning bool     `json:"-"`
}

type ExcludedEntry struct {
	ID    string
	Path  string
	Issue Issue
}

type ReadResult struct {
	Entries    []domain.Entry
	Issues     []Issue
	Excluded   []ExcludedEntry
	EntryPaths map[string]string
}

type ResolveError struct {
	Code       string
	Message    string
	Candidates []string
}

func (e *ResolveError) Error() string { return e.Message }

// SavedUnconfirmedError reports that an entry file was installed but its
// durability could not be confirmed. The entry exists; re-adding it would
// create a duplicate.
type SavedUnconfirmedError struct {
	ID   string
	Path string
	Err  error
}

func (e *SavedUnconfirmedError) Error() string {
	return fmt.Sprintf("saved %s at %s but could not confirm it is durable: %v; do not add it again", e.ID, e.Path, e.Err)
}

func (e *SavedUnconfirmedError) Unwrap() error { return e.Err }

// CreateResult is the outcome of a successful Create. Warnings describe
// problems that did not affect the saved entry.
type CreateResult struct {
	ID       string
	Path     string
	Warnings []string
}

func NewRepository(paths Paths) *Repository { return &Repository{Paths: paths} }

// Create stores a new entry. Error messages never contain the absolute data
// directory (design §9.2).
func (r *Repository) Create(entry domain.Entry) (CreateResult, error) {
	result, err := r.create(entry)
	return result, scrubDataPath(r.Paths.Data, err)
}

func (r *Repository) create(entry domain.Entry) (CreateResult, error) {
	if entry.ID == "" {
		return CreateResult{}, &ResolveError{Code: "validation_failed", Message: "entry ID is required before storage"}
	}
	if _, err := ulid.ParseStrict(strings.ToUpper(entry.ID)); err != nil {
		return CreateResult{}, &ResolveError{Code: "validation_failed", Message: "entry ID must be a valid ULID"}
	}
	entry.ID = strings.ToUpper(entry.ID)
	entry.Format = FormatVersion
	if entry.UpdatedAt.IsZero() {
		entry.UpdatedAt = entry.CreatedAt
	}
	entry.Tags = domain.NormalizeTags(entry.Tags)
	data, err := Serialize(entry)
	if err != nil {
		return CreateResult{}, err
	}
	if len(data) > MaxFileSize {
		return CreateResult{}, &ResolveError{Code: "validation_failed", Message: fmt.Sprintf("entry exceeds %d-byte file limit", MaxFileSize)}
	}
	monthDir := filepath.Join(r.Paths.EntriesDir(), entry.CreatedAt.Format("2006"), entry.CreatedAt.Format("01"))
	if err := mkdirAllDurable(monthDir); err != nil {
		return CreateResult{}, fmt.Errorf("create entry directory: %w", err)
	}
	path := filepath.Join(monthDir, entry.ID+".md")
	relative := relativePath(r.Paths.Data, path)
	warnings, err := atomicWrite(path, data)
	if err != nil {
		var unconfirmed *SavedUnconfirmedError
		if errors.As(err, &unconfirmed) {
			unconfirmed.ID, unconfirmed.Path = entry.ID, relative
		}
		return CreateResult{}, err
	}
	return CreateResult{ID: entry.ID, Path: relative, Warnings: warnings}, nil
}

// mkdirAllDurable creates missing directories one level at a time with mode
// 0700 and fsyncs the parent of each directory it creates, so a new
// directory entry survives a crash (Unix; a no-op on Windows).
func mkdirAllDurable(path string) error {
	path = filepath.Clean(path)
	var missing []string
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Stat(current)
		if err == nil {
			if !info.IsDir() {
				return fmt.Errorf("%s is not a directory", current)
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		missing = append(missing, current)
		if filepath.Dir(current) == current {
			break
		}
	}
	for i := len(missing) - 1; i >= 0; i-- {
		if err := os.Mkdir(missing[i], 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		if err := syncDir(filepath.Dir(missing[i])); err != nil {
			return fmt.Errorf("sync %s: %w", filepath.Dir(missing[i]), err)
		}
	}
	return nil
}

// List reads every entry file. Error messages never contain the absolute
// data directory (design §9.2).
func (r *Repository) List() (ReadResult, error) {
	result, err := r.list()
	return result, scrubDataPath(r.Paths.Data, err)
}

func (r *Repository) list() (ReadResult, error) {
	result := ReadResult{EntryPaths: make(map[string]string)}
	root := r.Paths.EntriesDir()
	info, err := os.Stat(root)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("open entries directory: %w", err)
	}
	if !info.IsDir() {
		return result, fmt.Errorf("entries path is not a directory")
	}
	var parsed []fileEntry
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			result.Issues = append(result.Issues, issueFromError(r.Paths.Data, path, walkErr))
			return nil
		}
		if d.IsDir() {
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") || filepath.Ext(d.Name()) != ".md" || d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		fileInfo, err := d.Info()
		if err != nil {
			result.Issues = append(result.Issues, issueFromError(r.Paths.Data, path, err))
			return nil
		}
		if !fileInfo.Mode().IsRegular() {
			return nil
		}
		if fileInfo.Size() > MaxFileSize {
			issue := Issue{Code: "parse_error", Path: relativePath(r.Paths.Data, path), Message: fmt.Sprintf("file exceeds %d bytes", MaxFileSize)}
			result.Issues = append(result.Issues, issue)
			result.Excluded = append(result.Excluded, ExcludedEntry{ID: filenameID(path), Path: issue.Path, Issue: issue})
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			issue := issueFromError(r.Paths.Data, path, err)
			result.Issues = append(result.Issues, issue)
			result.Excluded = append(result.Excluded, ExcludedEntry{ID: filenameID(path), Path: issue.Path, Issue: issue})
			return nil
		}
		entry, err := Parse(data)
		if err != nil {
			issue := issueFromParseError(r.Paths.Data, path, err)
			result.Issues = append(result.Issues, issue)
			result.Excluded = append(result.Excluded, ExcludedEntry{ID: filenameID(path), Path: issue.Path, Issue: issue})
			return nil
		}
		stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		if _, err := ulid.ParseStrict(strings.ToUpper(stem)); err != nil {
			result.Issues = append(result.Issues, Issue{Code: "filename_not_ulid", Path: relativePath(r.Paths.Data, path), Message: "filename is not a ULID", Warning: true})
		}
		if !strings.EqualFold(stem, entry.ID) {
			result.Issues = append(result.Issues, Issue{Code: "filename_mismatch", Path: relativePath(r.Paths.Data, path), Message: fmt.Sprintf("filename ID %q does not match front-matter ID %q", stem, entry.ID), Warning: true})
		}
		parsed = append(parsed, fileEntry{Entry: entry, Path: path, RelativePath: relativePath(r.Paths.Data, path), Stem: stem})
		return nil
	})
	if err != nil {
		return result, err
	}
	byID := make(map[string][]fileEntry)
	for _, item := range parsed {
		byID[item.Entry.ID] = append(byID[item.Entry.ID], item)
	}
	for id, items := range byID {
		if len(items) == 1 {
			result.Entries = append(result.Entries, items[0].Entry)
			result.EntryPaths[items[0].Entry.ID] = items[0].RelativePath
			continue
		}
		canonical := make([]fileEntry, 0, 1)
		for _, item := range items {
			if strings.EqualFold(item.Stem, id) {
				canonical = append(canonical, item)
			}
		}
		if len(canonical) == 1 {
			result.Entries = append(result.Entries, canonical[0].Entry)
			result.EntryPaths[canonical[0].Entry.ID] = canonical[0].RelativePath
			for _, item := range items {
				if item.Path == canonical[0].Path {
					continue
				}
				issue := Issue{Code: "duplicate_id", Path: item.RelativePath, Message: fmt.Sprintf("duplicate ID %s; canonical file is %s", id, canonical[0].RelativePath)}
				result.Issues = append(result.Issues, issue)
				result.Excluded = append(result.Excluded, ExcludedEntry{ID: id, Path: item.RelativePath, Issue: issue})
			}
			continue
		}
		paths := make([]string, 0, len(items))
		for _, item := range items {
			paths = append(paths, item.RelativePath)
		}
		sort.Strings(paths)
		issue := Issue{Code: "duplicate_id", Paths: paths, Path: paths[0], Message: fmt.Sprintf("duplicate ID %s across %d files", id, len(paths))}
		result.Issues = append(result.Issues, issue)
		for _, item := range items {
			result.Excluded = append(result.Excluded, ExcludedEntry{ID: id, Path: item.RelativePath, Issue: issue})
		}
	}
	sort.Slice(result.Entries, func(i, j int) bool {
		if result.Entries[i].CreatedAt.Equal(result.Entries[j].CreatedAt) {
			return result.Entries[i].ID > result.Entries[j].ID
		}
		return result.Entries[i].CreatedAt.After(result.Entries[j].CreatedAt)
	})
	return result, nil
}

func (r *Repository) Resolve(input string) (domain.Entry, ReadResult, error) {
	normalized, err := normalizeIDInput(input)
	if err != nil {
		return domain.Entry{}, ReadResult{}, &ResolveError{Code: "usage", Message: err.Error()}
	}
	result, err := r.List()
	if err != nil {
		return domain.Entry{}, result, err
	}
	if len(normalized) == ulid.EncodedSize {
		for _, entry := range result.Entries {
			if entry.ID == normalized {
				return entry, result, nil
			}
		}
	}
	var matches []domain.Entry
	for _, entry := range result.Entries {
		if strings.HasPrefix(entry.ID, normalized) {
			matches = append(matches, entry)
		}
	}
	if len(matches) == 1 {
		return matches[0], result, nil
	}
	if len(matches) > 1 {
		candidates := make([]string, 0, len(matches))
		for i, entry := range matches {
			candidates = append(candidates, shortestUniquePrefix(entry.ID, matches, i))
			if len(candidates) == 5 {
				break
			}
		}
		message := fmt.Sprintf("entry prefix %q is ambiguous; candidates: %s", input, strings.Join(candidates, ", "))
		if len(matches) > len(candidates) {
			message += fmt.Sprintf(" (and %d more)", len(matches)-len(candidates))
		}
		return domain.Entry{}, result, &ResolveError{Code: "ambiguous_id", Candidates: candidates, Message: message}
	}
	for _, excluded := range result.Excluded {
		if strings.HasPrefix(excluded.ID, normalized) || strings.HasPrefix(strings.ToUpper(strings.TrimSuffix(filepath.Base(excluded.Path), filepath.Ext(excluded.Path))), normalized) {
			return domain.Entry{}, result, &ResolveError{Code: "unreadable_entry", Message: fmt.Sprintf("entry exists but cannot be used: %s: %s", excluded.Path, excluded.Issue.Message)}
		}
	}
	return domain.Entry{}, result, &ResolveError{Code: "not_found", Message: fmt.Sprintf("entry %q not found", input)}
}

type fileEntry struct {
	Entry        domain.Entry
	Path         string
	RelativePath string
	Stem         string
}

func normalizeIDInput(input string) (string, error) {
	value := strings.ToUpper(strings.TrimSpace(input))
	if len(value) < 4 || len(value) > ulid.EncodedSize {
		return "", fmt.Errorf("ID or prefix must be 4–26 Crockford base32 characters")
	}
	for _, r := range value {
		if !strings.ContainsRune("0123456789ABCDEFGHJKMNPQRSTVWXYZ", r) {
			return "", fmt.Errorf("invalid ID or prefix %q", input)
		}
	}
	return value, nil
}

func shortestUniquePrefix(id string, matches []domain.Entry, index int) string {
	for n := 8; n <= len(id); n++ {
		prefix := id[:n]
		unique := true
		for j, other := range matches {
			if j != index && strings.HasPrefix(other.ID, prefix) {
				unique = false
				break
			}
		}
		if unique {
			return prefix
		}
	}
	return id
}

func relativePath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

func filenameID(path string) string {
	return strings.ToUpper(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
}

func issueFromError(root, path string, err error) Issue {
	return Issue{Code: "parse_error", Path: relativePath(root, path), Message: scrubMessage(root, err.Error())}
}

// scrubbedError replaces an error's message while keeping the original error
// available to errors.Is and errors.As.
type scrubbedError struct {
	err error
	msg string
}

func (e *scrubbedError) Error() string { return e.msg }

func (e *scrubbedError) Unwrap() error { return e.err }

// scrubDataPath hides the absolute data directory in err's message, so output
// never exposes the user's home path.
func scrubDataPath(root string, err error) error {
	if err == nil {
		return nil
	}
	message := scrubMessage(root, err.Error())
	if message == err.Error() {
		return err
	}
	return &scrubbedError{err: err, msg: message}
}

// scrubMessage rewrites paths inside the data directory as relative paths.
func scrubMessage(root, message string) string {
	root = filepath.Clean(root)
	message = strings.ReplaceAll(message, root+string(filepath.Separator), "")
	return strings.ReplaceAll(message, root, ".")
}

func issueFromParseError(root, path string, err error) Issue {
	issue := issueFromError(root, path, err)
	var parseErr *ParseError
	if errors.As(err, &parseErr) {
		issue.Code = parseErr.Code
		issue.Line = parseErr.Line
		issue.Message = parseErr.Message
	}
	return issue
}

var installFile = installNoReplaceFile
var linkFile = os.Link
var renameFile = noReplaceRename
var removeFile = os.Remove
var syncDir = syncDirectory

// atomicWrite installs data at path without ever replacing an existing file.
// Before installation any failure removes the temporary file and returns an
// error. After installation the entry exists, so cleanup problems become
// warnings and a failed directory sync becomes a SavedUnconfirmedError.
func atomicWrite(path string, data []byte) ([]string, error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".kuro-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("create temporary entry file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if err := tmp.Chmod(0o600); err != nil {
		cleanup()
		return nil, fmt.Errorf("set entry permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return nil, fmt.Errorf("write entry: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return nil, fmt.Errorf("flush entry: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return nil, fmt.Errorf("close entry: %w", err)
	}
	if err := installFile(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return nil, err
	}
	var warnings []string
	// After a hard-link install the temporary name still exists; after a
	// rename it is already gone.
	if err := removeFile(tmpName); err != nil && !errors.Is(err, os.ErrNotExist) {
		warnings = append(warnings, fmt.Sprintf("entry saved, but the temporary file %s could not be removed: %v", filepath.Base(tmpName), err))
	}
	if err := syncDir(filepath.Dir(path)); err != nil {
		return warnings, &SavedUnconfirmedError{Err: err}
	}
	return warnings, nil
}

func installNoReplaceFile(temp, final string) error {
	if err := linkFile(temp, final); err == nil {
		return nil
	} else {
		if isTargetExists(err) {
			return &ResolveError{Code: "target_exists", Message: fmt.Sprintf("entry file already exists: %s", final)}
		}
		if !isLinkUnsupported(err) {
			return fmt.Errorf("install entry: %w", err)
		}
	}
	if err := renameFile(temp, final); err != nil {
		if isTargetExists(err) {
			return &ResolveError{Code: "target_exists", Message: fmt.Sprintf("entry file already exists: %s", final)}
		}
		if isRenameUnsupported(err) {
			return &ResolveError{Code: "io_error", Message: "cannot install without overwriting"}
		}
		return fmt.Errorf("install entry: %w", err)
	}
	return nil
}

func fileHash(path string) ([32]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return [32]byte{}, err
	}
	defer file.Close()
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return [32]byte{}, err
	}
	var result [32]byte
	copy(result[:], h.Sum(nil))
	return result, nil
}
