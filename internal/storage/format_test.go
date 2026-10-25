package storage

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"kuro/internal/domain"
)

func testEntry() domain.Entry {
	when := time.Date(2026, 10, 24, 14, 32, 0, 0, time.FixedZone("EDT", -4*60*60))
	return domain.Entry{ID: "01JB7Q2M8X4T9K3V5N6P0R1S2A", Format: FormatVersion, Type: domain.TypeNote, Title: "A: useful #note", Body: "line one\nline two — Привет", CreatedAt: when, UpdatedAt: when, Project: "api", Tags: []string{"auth", "concurrency"}, Source: "manual"}
}

func TestSerializeParseRoundTrip(t *testing.T) {
	original := testEntry()
	data, err := Serialize(original)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(data)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if parsed.ID != original.ID || parsed.Title != original.Title || parsed.Body != original.Body || parsed.Project != original.Project || parsed.CreatedAt.Format(time.RFC3339Nano) != original.CreatedAt.Format(time.RFC3339Nano) {
		t.Fatalf("round trip changed entry: %#v", parsed)
	}
}

func TestParseLongLineAndCRLFAndBOM(t *testing.T) {
	body := strings.Repeat("x", 200*1024)
	data := append([]byte{0xef, 0xbb, 0xbf}, []byte("---\r\nformat: 1\r\nid: 01JB7Q2M8X4T9K3V5N6P0R1S2A\r\ntype: note\r\ncreated_at: 2026-10-24T14:32:00-04:00\r\n---\r\n\r\n")...)
	data = append(data, []byte(body)...)
	entry, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Body != body {
		t.Fatalf("long or CRLF body changed: got %d bytes", len(entry.Body))
	}
	if entry.UpdatedAt != entry.CreatedAt {
		t.Fatal("updated_at fallback should equal created_at")
	}
}

func TestParseInvalidCases(t *testing.T) {
	base := "---\nformat: 1\nid: 01JB7Q2M8X4T9K3V5N6P0R1S2A\ntype: note\ncreated_at: 2026-10-24T14:32:00-04:00\n---\n\nbody"
	cases := []struct {
		name string
		data string
		code string
	}{
		{"bad format", strings.Replace(base, "format: 1", "format: 1abc", 1), "unsupported_format"},
		{"duplicate key", strings.Replace(base, "type: note", "type: note\ntype: note", 1), "duplicate_key"},
		{"missing type", strings.Replace(base, "type: note\n", "", 1), "missing_required"},
		{"bad id", strings.Replace(base, "01JB7Q2M8X4T9K3V5N6P0R1S2A", "not-an-id", 1), "invalid_id"},
		{"missing delimiter", strings.Replace(base, "---\n\nbody", "", 1), "invalid_front_matter"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.data))
			if err == nil {
				t.Fatal("expected parse error")
			}
			var parseErr *ParseError
			if !asParseError(err, &parseErr) || parseErr.Code != tc.code {
				t.Fatalf("got %T %v, want code %q", err, err, tc.code)
			}
		})
	}
}

func TestParseListsAndUnknownKeys(t *testing.T) {
	data := `---
format: 1
id: 01JB7Q2M8X4T9K3V5N6P0R1S2A
type: note
created_at: 2026-10-24T14:32:00-04:00
tags:
  - auth
  - testing
x_ticket: API-142
---

body
`
	entry, err := Parse([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(entry.Tags, ",") != "auth,testing" {
		t.Fatalf("unexpected tags: %#v", entry.Tags)
	}
}

func TestParseDoesNotTreatBodyDelimiterAsFrontMatter(t *testing.T) {
	data, _ := Serialize(testEntry())
	data = append(data, []byte("---\nsecond line\n")...)
	entry, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains([]byte(entry.Body), []byte("---\nsecond line")) {
		t.Fatal("body delimiter was not preserved")
	}
}

func asParseError(err error, target **ParseError) bool {
	value, ok := err.(*ParseError)
	if ok {
		*target = value
	}
	return ok
}

func TestParseErrorsReportTheKeyLine(t *testing.T) {
	base := "---\nformat: 1\nid: 01JB7Q2M8X4T9K3V5N6P0R1S2A\ntype: note\ncreated_at: 2026-10-24T14:32:00-04:00\nproject: api\n---\n\nbody"
	cases := []struct {
		name string
		data string
		line int
	}{
		{"format", strings.Replace(base, "format: 1", "format: 2", 1), 2},
		{"id", strings.Replace(base, "01JB7Q2M8X4T9K3V5N6P0R1S2A", "not-an-id", 1), 3},
		{"type", strings.Replace(base, "type: note", "type: memo", 1), 4},
		{"created_at", strings.Replace(base, "2026-10-24T14:32:00-04:00", "yesterday", 1), 5},
		{"project", strings.Replace(base, "project: api", "project: no_underscores", 1), 6},
		{"body NUL", base + "\nsecond\x00line", 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.data))
			var parseErr *ParseError
			if !asParseError(err, &parseErr) || parseErr.Line != tc.line {
				t.Fatalf("got %v, want line %d", err, tc.line)
			}
		})
	}
}

func TestParseNormalizesProjectAndTagsOnRead(t *testing.T) {
	data := "---\nformat: 1\nid: 01JB7Q2M8X4T9K3V5N6P0R1S2A\ntype: note\ncreated_at: 2026-10-24T14:32:00-04:00\nproject: API\ntags: [Auth, \"#auth\", Testing]\n---\n\nbody"
	entry, err := Parse([]byte(data))
	if err != nil {
		t.Fatalf("hand-edited capitals must not exclude the note: %v", err)
	}
	if entry.Project != "api" || strings.Join(entry.Tags, ",") != "auth,testing" {
		t.Fatalf("got project %q tags %v", entry.Project, entry.Tags)
	}
}
