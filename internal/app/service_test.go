package app

import (
	"testing"
	"time"

	"kuro/internal/domain"
	"kuro/internal/storage"
)

type fakeRepo struct{ created domain.Entry }

func (r *fakeRepo) Create(entry domain.Entry) (CreateResult, error) {
	r.created = entry
	return CreateResult{Path: "entries/2026/10/" + entry.ID + ".md", Warnings: []string{"cleanup warning"}}, nil
}

func (r *fakeRepo) List() (Collection, error) { return Collection{}, nil }

func (r *fakeRepo) Resolve(string) (domain.Entry, Collection, error) {
	return domain.Entry{}, Collection{}, nil
}

type fixedClock struct{ value time.Time }

func (c fixedClock) Now() time.Time { return c.value }

type fixedID string

func (id fixedID) New(time.Time) (string, error) { return string(id), nil }

func TestAddUsesInjectedClockAndIDGenerator(t *testing.T) {
	now := time.Date(2026, 10, 25, 10, 0, 0, 0, time.FixedZone("IST", 19800))
	backdated := time.Date(2026, 10, 24, 14, 32, 0, 0, time.FixedZone("EDT", -4*60*60))
	repo := &fakeRepo{}
	service := &Service{Repo: repo, Clock: fixedClock{now}, IDGenerator: fixedID("01JB7Q2M8X4T9K3V5N6P0R1S2A")}
	result, err := service.Add("title", "body", domain.TypeNote, "API", []string{"#Auth", "testing"}, &backdated)
	if err != nil {
		t.Fatal(err)
	}
	entry := result.Entry
	if result.Path != "entries/2026/10/01JB7Q2M8X4T9K3V5N6P0R1S2A.md" || len(result.Warnings) != 1 {
		t.Fatalf("repository path and warnings not passed through: %#v", result)
	}
	if entry.CreatedAt.Format(time.RFC3339Nano) != backdated.Format(time.RFC3339Nano) {
		t.Fatalf("backdated offset changed: %s", entry.CreatedAt)
	}
	if !entry.CreatedAt.Equal(backdated) || !entry.UpdatedAt.Equal(backdated) {
		t.Fatal("new entry timestamps should match created timestamp")
	}
	if entry.Project != "api" || len(entry.Tags) != 2 || entry.Tags[0] != "auth" {
		t.Fatalf("normalization failed: %#v", entry)
	}
	if repo.created.ID != entry.ID || repo.created.Format != storage.FormatVersion {
		t.Fatal("repository received the wrong identity or format")
	}
}
