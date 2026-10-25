package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestValidateCapture(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.FixedZone("IST", 19800))
	if err := ValidateCapture("title", "body", TypeNote, "API", []string{"#Auth"}, now, false); err != nil {
		t.Fatalf("valid normalized input rejected: %v", err)
	}
	if !errors.Is(ValidateCapture("", "", TypeNote, "", nil, now, false), ErrEmptyEntry) {
		t.Fatal("empty entry should be rejected")
	}
	if !errors.Is(ValidateCapture("bad\ntitle", "body", TypeNote, "", nil, now, false), ErrInvalidTitle) {
		t.Fatal("multi-line title should be rejected")
	}
	if !errors.Is(ValidateCapture("title", "body", "task", "", nil, now, false), ErrInvalidType) {
		t.Fatal("task capture should be deferred from Phase 1")
	}
	if !errors.Is(ValidateCapture("title", "body", TypeNote, "bad project", nil, now, false), ErrInvalidProject) {
		t.Fatal("invalid project should be rejected")
	}
	if !errors.Is(ValidateCapture("title", "body", TypeNote, "", []string{"bad tag!"}, now, false), ErrInvalidTag) {
		t.Fatal("invalid tag should be rejected")
	}
	if !errors.Is(ValidateCapture("title", "body\x00", TypeNote, "", nil, now, false), ErrInvalidBody) {
		t.Fatal("NUL body should be rejected")
	}
	if err := ValidateCapture(strings.Repeat("🙂", MaxTitleRunes), "", TypeNote, "", nil, now, false); err != nil {
		t.Fatalf("200 Unicode title rejected: %v", err)
	}
	if errors.Is(ValidateCapture(strings.Repeat("🙂", MaxTitleRunes+1), "", TypeNote, "", nil, now, false), ErrInvalidTitle) == false {
		t.Fatal("201 Unicode title should be rejected")
	}
}

func TestEntryValidationFutureTimestamp(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.FixedZone("IST", 19800))
	entry := Entry{Type: TypeNote, Body: "hello", CreatedAt: now.Add(2 * time.Hour), UpdatedAt: now}
	if !errors.Is(entry.Validate(now), ErrFutureTimestamp) {
		t.Fatal("future entry should be rejected")
	}
}

func TestNormalizeTags(t *testing.T) {
	got := NormalizeTags([]string{"#Auth", "auth", " concurrency ", "api/go"})
	want := []string{"api/go", "auth", "concurrency"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}
