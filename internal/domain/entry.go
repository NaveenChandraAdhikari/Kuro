package domain

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	ErrEmptyEntry      = errors.New("entry needs a title or body")
	ErrInvalidType     = errors.New("invalid entry type")
	ErrFutureTimestamp = errors.New("entry timestamp cannot be in the future")
	ErrInvalidTitle    = errors.New("invalid title")
	ErrInvalidProject  = errors.New("invalid project")
	ErrInvalidTag      = errors.New("invalid tag")
	ErrInvalidBody     = errors.New("invalid body")
	ErrInvalidStatus   = errors.New("invalid status")
	ErrInvalidPriority = errors.New("invalid priority")
	ErrInvalidSource   = errors.New("invalid source")
	ErrMissingTitle    = errors.New("missing title")
	ErrMissingStatus   = errors.New("missing status")
)

const (
	TypeNote    = "note"
	TypeJournal = "journal"
	TypeTask    = "task"

	MaxTitleRunes = 200
	MaxFieldRunes = 64
)

var validTypes = map[string]bool{
	TypeNote: true, TypeJournal: true, TypeTask: true,
	"solution": true, "checkpoint": true, "review": true,
}

var phaseOneTypes = map[string]bool{TypeNote: true, TypeJournal: true}

type Entry struct {
	ID          string
	Format      int
	Type        string
	Title       string
	Body        string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Project     string
	Tags        []string
	Status      string
	Priority    string
	CompletedAt *time.Time
	Links       []string
	Source      string
	GitRepo     string
	GitBranch   string
	GitCommit   string
	DeletedAt   *time.Time
}

func (e Entry) Validate(now time.Time) error {
	if err := ValidateCapture(e.Title, e.Body, e.Type, e.Project, e.Tags, now, false); err != nil {
		return err
	}
	if !e.CreatedAt.IsZero() && e.CreatedAt.After(now.Add(time.Minute)) {
		return fmt.Errorf("%w: %s", ErrFutureTimestamp, e.CreatedAt.Format(time.RFC3339Nano))
	}
	return nil
}

// ValidateCapture validates values accepted by the Phase 1 add command.
// The body limit that depends on serialized front matter is enforced by storage.
func ValidateCapture(title, body, entryType, project string, tags []string, _ time.Time, allowAllTypes bool) error {
	if !utf8.ValidString(title) || !utf8.ValidString(body) {
		return ErrInvalidBody
	}
	if strings.ContainsRune(body, '\x00') {
		return fmt.Errorf("%w: body contains NUL", ErrInvalidBody)
	}
	if err := validateSingleLine(title, "title"); err != nil {
		return err
	}
	trimmedTitle := strings.TrimSpace(title)
	if strings.TrimSpace(trimmedTitle) == "" && strings.TrimSpace(body) == "" {
		return ErrEmptyEntry
	}
	if utf8.RuneCountInString(trimmedTitle) > MaxTitleRunes {
		return fmt.Errorf("%w: title exceeds %d Unicode characters", ErrInvalidTitle, MaxTitleRunes)
	}
	if allowAllTypes {
		if !validTypes[entryType] {
			return fmt.Errorf("%w: %q", ErrInvalidType, entryType)
		}
	} else if !phaseOneTypes[entryType] {
		return fmt.Errorf("%w: %q (Phase 1 accepts note or journal)", ErrInvalidType, entryType)
	}
	if project != "" {
		if err := validateSingleLine(project, "project"); err != nil {
			return err
		}
		if err := validateProject(NormalizeProject(project)); err != nil {
			return err
		}
	}
	for _, rawTag := range tags {
		if err := validateSingleLine(rawTag, "tag"); err != nil {
			return err
		}
		if err := validateTag(strings.ToLower(strings.TrimPrefix(strings.TrimSpace(rawTag), "#"))); err != nil {
			return err
		}
	}
	return nil
}

func validateSingleLine(value, field string) error {
	for _, r := range value {
		if unicode.IsControl(r) {
			var kind error
			switch field {
			case "title":
				kind = ErrInvalidTitle
			case "project":
				kind = ErrInvalidProject
			case "tag":
				kind = ErrInvalidTag
			default:
				kind = ErrInvalidBody
			}
			return fmt.Errorf("%w: %s contains control characters", kind, field)
		}
	}
	return nil
}

func validateProject(project string) error {
	if project != strings.ToLower(project) || utf8.RuneCountInString(project) > MaxFieldRunes || project == "" {
		return fmt.Errorf("%w: must be lowercase and 1-%d characters", ErrInvalidProject, MaxFieldRunes)
	}
	for _, r := range project {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' {
			return fmt.Errorf("%w: %q", ErrInvalidProject, project)
		}
	}
	return nil
}

func validateTag(tag string) error {
	tag = strings.TrimPrefix(strings.TrimSpace(tag), "#")
	if tag == "" || utf8.RuneCountInString(tag) > MaxFieldRunes || tag != strings.ToLower(tag) {
		return fmt.Errorf("%w: %q", ErrInvalidTag, tag)
	}
	for _, r := range tag {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_' && r != '-' && r != '/' {
			return fmt.Errorf("%w: %q", ErrInvalidTag, tag)
		}
	}
	return nil
}

func ValidateRead(e Entry) error {
	if strings.ContainsRune(e.Body, '\x00') {
		return fmt.Errorf("%w: body contains NUL", ErrInvalidBody)
	}
	if !validTypes[e.Type] {
		return fmt.Errorf("%w: %q", ErrInvalidType, e.Type)
	}
	if e.Title != "" {
		if err := validateSingleLine(e.Title, "title"); err != nil {
			return err
		}
		if utf8.RuneCountInString(e.Title) > MaxTitleRunes {
			return fmt.Errorf("%w: title exceeds %d Unicode characters", ErrInvalidTitle, MaxTitleRunes)
		}
	}
	if strings.TrimSpace(e.Title) == "" && strings.TrimSpace(e.Body) == "" {
		return ErrEmptyEntry
	}
	if e.Type == TypeTask || e.Type == "checkpoint" {
		if e.Status == "" {
			return fmt.Errorf("%w: %s entries require status", ErrMissingStatus, e.Type)
		}
	}
	if e.Type == TypeTask || e.Type == "solution" || e.Type == "checkpoint" || e.Type == "review" {
		if strings.TrimSpace(e.Title) == "" {
			return fmt.Errorf("%w: %s entries require a title", ErrMissingTitle, e.Type)
		}
	}
	if e.Project != "" {
		if err := validateProject(e.Project); err != nil {
			return err
		}
	}
	for _, tag := range e.Tags {
		if err := validateTag(tag); err != nil {
			return err
		}
	}
	if e.Status != "" && e.Status != "open" && e.Status != "done" && e.Status != "archived" {
		return fmt.Errorf("%w: %q", ErrInvalidStatus, e.Status)
	}
	if e.Priority != "" && e.Priority != "low" && e.Priority != "normal" && e.Priority != "high" {
		return fmt.Errorf("%w: %q", ErrInvalidPriority, e.Priority)
	}
	if e.Source != "" && e.Source != "manual" && e.Source != "stdin" && e.Source != "editor" && e.Source != "git" {
		return fmt.Errorf("%w: %q", ErrInvalidSource, e.Source)
	}
	return nil
}

// NormalizeProject applies the project normalisation used on capture and read.
func NormalizeProject(project string) string { return strings.ToLower(strings.TrimSpace(project)) }

func NormalizeTags(tags []string) []string {
	seen := make(map[string]struct{}, len(tags))
	result := make([]string, 0, len(tags))
	for _, raw := range tags {
		tag := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(raw), "#"))
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		result = append(result, tag)
	}
	sort.Strings(result)
	return result
}
