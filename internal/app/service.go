package app

import (
	"crypto/rand"
	"fmt"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	"kuro/internal/domain"
	"kuro/internal/storage"
)

type Repository interface {
	Create(domain.Entry) (CreateResult, error)
	List() (Collection, error)
	Resolve(string) (domain.Entry, Collection, error)
}

// CreateResult is the outcome of storing a new entry. Path is relative to the
// data directory; Warnings describe problems that did not affect the entry.
type CreateResult struct {
	Path     string
	Warnings []string
}

// AddResult is the outcome of Service.Add.
type AddResult struct {
	Entry    domain.Entry
	Path     string
	Warnings []string
}

type Issue struct {
	Code    string
	Path    string
	Paths   []string
	Line    int
	Message string
	Warning bool
}

type ExcludedEntry struct {
	ID    string
	Path  string
	Issue Issue
}

type Collection struct {
	Entries    []domain.Entry
	Issues     []Issue
	Excluded   []ExcludedEntry
	EntryPaths map[string]string
}

type FileRepository struct{ repo *storage.Repository }

func NewFileRepository(paths storage.Paths) *FileRepository {
	return &FileRepository{repo: storage.NewRepository(paths)}
}

func (r *FileRepository) Create(entry domain.Entry) (CreateResult, error) {
	result, err := r.repo.Create(entry)
	return CreateResult{Path: result.Path, Warnings: result.Warnings}, err
}

func (r *FileRepository) List() (Collection, error) {
	result, err := r.repo.List()
	return convertCollection(result), err
}

func (r *FileRepository) Resolve(id string) (domain.Entry, Collection, error) {
	entry, result, err := r.repo.Resolve(id)
	return entry, convertCollection(result), err
}

func convertCollection(result storage.ReadResult) Collection {
	issues := make([]Issue, 0, len(result.Issues))
	for _, issue := range result.Issues {
		issues = append(issues, Issue{Code: issue.Code, Path: issue.Path, Paths: append([]string(nil), issue.Paths...), Line: issue.Line, Message: issue.Message, Warning: issue.Warning})
	}
	excluded := make([]ExcludedEntry, 0, len(result.Excluded))
	for _, item := range result.Excluded {
		excluded = append(excluded, ExcludedEntry{ID: item.ID, Path: item.Path, Issue: Issue{Code: item.Issue.Code, Path: item.Issue.Path, Paths: append([]string(nil), item.Issue.Paths...), Line: item.Issue.Line, Message: item.Issue.Message, Warning: item.Issue.Warning}})
	}
	return Collection{Entries: result.Entries, Issues: issues, Excluded: excluded, EntryPaths: result.EntryPaths}
}

type Clock interface{ Now() time.Time }

type IDGenerator interface {
	New(time.Time) (string, error)
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().Local() }

type ULIDGenerator struct{}

func (ULIDGenerator) New(at time.Time) (string, error) {
	id, err := ulid.New(ulid.Timestamp(at), rand.Reader)
	if err != nil {
		return "", fmt.Errorf("generate entry ID: %w", err)
	}
	return id.String(), nil
}

type Service struct {
	Repo        Repository
	Clock       Clock
	IDGenerator IDGenerator
}

func (s *Service) now() time.Time {
	if s.Clock != nil {
		return s.Clock.Now()
	}
	return SystemClock{}.Now()
}

func (s *Service) idGenerator() IDGenerator {
	if s.IDGenerator != nil {
		return s.IDGenerator
	}
	return ULIDGenerator{}
}

func (s *Service) Add(title, body, entryType, project string, tags []string, at *time.Time) (AddResult, error) {
	now := s.now()
	created := now
	if at != nil {
		created = *at
	}
	normalizedProject := domain.NormalizeProject(project)
	normalizedTags := domain.NormalizeTags(tags)
	if err := domain.ValidateCapture(title, body, entryType, project, tags, now, false); err != nil {
		return AddResult{}, err
	}
	if created.After(now.Add(time.Minute)) {
		return AddResult{}, fmt.Errorf("%w: %s", domain.ErrFutureTimestamp, created.Format(time.RFC3339Nano))
	}
	id, err := s.idGenerator().New(created)
	if err != nil {
		return AddResult{}, err
	}
	entry := domain.Entry{
		ID:        strings.ToUpper(id),
		Format:    storage.FormatVersion,
		Type:      entryType,
		Title:     strings.TrimSpace(title),
		Body:      body,
		CreatedAt: created,
		UpdatedAt: created,
		Project:   normalizedProject,
		Tags:      normalizedTags,
		Source:    "manual",
	}
	if err := entry.Validate(now); err != nil {
		return AddResult{}, err
	}
	stored, err := s.Repo.Create(entry)
	if err != nil {
		return AddResult{}, err
	}
	return AddResult{Entry: entry, Path: stored.Path, Warnings: stored.Warnings}, nil
}

func (s *Service) List() (Collection, error) { return s.Repo.List() }

func (s *Service) Show(id string) (domain.Entry, Collection, error) {
	if strings.TrimSpace(id) == "" {
		return domain.Entry{}, Collection{}, &storage.ResolveError{Code: "usage", Message: "entry ID is required"}
	}
	return s.Repo.Resolve(id)
}
