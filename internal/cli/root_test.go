package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"charm.land/fang/v2"
	"github.com/spf13/cobra"
)

func TestPhaseOneCLIJSONFlow(t *testing.T) {
	data := t.TempDir()
	t.Setenv("KURO_DATA_DIR", data)
	t.Setenv("KURO_CONFIG_DIR", filepath.Join(data, "config"))
	t.Setenv("KURO_CACHE_DIR", filepath.Join(data, "cache"))

	root, err := NewRoot()
	if err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&stderr)
	root.SetArgs([]string{"add", "Investigated refresh", "--project", "API", "--tag", "#Auth", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var addPayload map[string]any
	if err := json.Unmarshal(out.Bytes(), &addPayload); err != nil {
		t.Fatalf("invalid add JSON: %v\n%s", err, out.String())
	}
	if addPayload["schema_version"] != float64(1) {
		t.Fatal("missing schema version")
	}
	out.Reset()
	root.SetArgs([]string{"list", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), `"errors": null`) || strings.Contains(out.String(), `"warnings": null`) {
		t.Fatalf("problem arrays must be arrays: %s", out.String())
	}
	if !strings.Contains(out.String(), `"project": "api"`) || !strings.Contains(out.String(), `"auth"`) {
		t.Fatalf("normalized fields missing: %s", out.String())
	}
	out.Reset()
	root.SetArgs([]string{"status", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"error_count": 0`) || !strings.Contains(out.String(), `"warning_count": 0`) {
		t.Fatalf("status counts missing: %s", out.String())
	}
}

func TestCLIRejectsTitleInjectionWithoutWriting(t *testing.T) {
	data := t.TempDir()
	t.Setenv("KURO_DATA_DIR", data)
	t.Setenv("KURO_CONFIG_DIR", filepath.Join(data, "config"))
	t.Setenv("KURO_CACHE_DIR", filepath.Join(data, "cache"))
	root, err := NewRoot()
	if err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"add", "body", "--title", "bad\ntitle"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected title validation error")
	}
	entries := filepath.Join(data, "entries")
	if _, err := os.Stat(entries); !os.IsNotExist(err) {
		t.Fatalf("entry directory should not be created: %v", err)
	}
}

func TestCLITextOutputSanitizesTerminalControls(t *testing.T) {
	data := t.TempDir()
	t.Setenv("KURO_DATA_DIR", data)
	t.Setenv("KURO_CONFIG_DIR", filepath.Join(data, "config"))
	t.Setenv("KURO_CACHE_DIR", filepath.Join(data, "cache"))
	path := filepath.Join(data, "entries", "2026", "10", "01JB7Q2M8X4T9K3V5N6P0R1S2A.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	content := "---\nformat: 1\nid: 01JB7Q2M8X4T9K3V5N6P0R1S2A\ntype: note\ncreated_at: 2026-10-24T14:32:00-04:00\n---\n\nhello \x1b[31mred\x1b[0m\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := NewRoot()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"show", "01JB7Q2M"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "\x1b") || strings.Contains(out.String(), "[31m") {
		t.Fatalf("terminal controls were not sanitized: %q", out.String())
	}
}

func newTestRoot(t *testing.T) (root *cobra.Command, out, stderr *bytes.Buffer, data string) {
	t.Helper()
	data = t.TempDir()
	t.Setenv("KURO_DATA_DIR", data)
	t.Setenv("KURO_CONFIG_DIR", filepath.Join(data, "config"))
	t.Setenv("KURO_CACHE_DIR", filepath.Join(data, "cache"))
	root, err := NewRoot()
	if err != nil {
		t.Fatal(err)
	}
	out, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(stderr)
	return root, out, stderr, data
}

func writeRawEntry(t *testing.T, data, relative, content string) {
	t.Helper()
	path := filepath.Join(data, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCLIListIssueJSONMatchesContract(t *testing.T) {
	root, out, _, data := newTestRoot(t)
	writeRawEntry(t, data, "entries/2026/10/01JB7Q2M8X4T9K3V5N6P0R1S2A.md",
		"---\nformat: 1\nid: not-an-id\ntype: note\ncreated_at: 2026-10-24T14:32:00-04:00\n---\n\nbody\n")
	writeRawEntry(t, data, "entries/2026/10/renamed.md",
		"---\nformat: 1\nid: 01JB7Q2M8X4T9K3V5N6P0R1S2B\ntype: note\ncreated_at: 2026-10-24T14:32:00-04:00\n---\n\nbody\n")
	root.SetArgs([]string{"list", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("per-file problems must not fail list: %v", err)
	}
	var payload struct {
		Errors   []map[string]any `json:"errors"`
		Warnings []map[string]any `json:"warnings"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Errors) != 1 || len(payload.Warnings) != 2 {
		t.Fatalf("got errors %v warnings %v", payload.Errors, payload.Warnings)
	}
	issue := payload.Errors[0]
	if keys := sortedKeys(issue); keys != "code,line,message,path" {
		t.Fatalf("issue keys = %s, want code,line,message,path", keys)
	}
	if issue["code"] != "parse_error" || issue["line"] != float64(3) || issue["path"] != "entries/2026/10/01JB7Q2M8X4T9K3V5N6P0R1S2A.md" {
		t.Fatalf("unexpected issue: %v", issue)
	}
	codes := map[any]bool{}
	for _, warning := range payload.Warnings {
		codes[warning["code"]] = true
	}
	if !codes["filename_mismatch"] || !codes["filename_not_ulid"] {
		t.Fatalf("unexpected warning codes: %v", payload.Warnings)
	}
}

func sortedKeys(m map[string]any) string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

func TestCLIUsageErrorsExitTwo(t *testing.T) {
	cases := [][]string{
		{"add", "--bogus", "body"},
		{"bogus"},
		{"list", "extra"},
		{"--json=maybe", "list"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			root, _, _, _ := newTestRoot(t)
			root.SetArgs(args)
			err := root.Execute()
			if err == nil {
				t.Fatal("expected usage error")
			}
			if errorCode(err) != "usage" || ExitCode(err) != 2 {
				t.Fatalf("got code %q exit %d for %v", errorCode(err), ExitCode(err), err)
			}
		})
	}
}

func TestCLIUsageErrorRendersAsJSONWhenRequested(t *testing.T) {
	root, _, _, _ := newTestRoot(t)
	root.SetArgs([]string{"add", "--bogus", "--json"})
	err := root.Execute()
	if !jsonRequested([]string{"add", "--bogus", "--json"}) || jsonRequested([]string{"add", "--", "--json"}) {
		t.Fatal("jsonRequested should honour --json before -- only")
	}
	var stderr bytes.Buffer
	errorHandler(&Root{}, true)(&stderr, fang.Styles{}, err)
	var payload fatalDTO
	if err := json.Unmarshal(stderr.Bytes(), &payload); err != nil {
		t.Fatalf("error output is not JSON: %v\n%s", err, stderr.String())
	}
	if payload.SchemaVersion != 1 || payload.Error.Code != "usage" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

func TestCLIRootWithoutArgsPrintsHelp(t *testing.T) {
	root, out, _, _ := newTestRoot(t)
	root.SetArgs(nil)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Local-first developer memory") {
		t.Fatalf("expected help text, got %q", out.String())
	}
}

func TestCLITagFlagNeverDropsInput(t *testing.T) {
	root, _, _, data := newTestRoot(t)
	root.SetArgs([]string{"add", "body", "--tag", "first\nsecond"})
	if err := root.Execute(); err == nil || errorCode(err) != "validation_failed" {
		t.Fatalf("a tag containing a newline must be rejected, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(data, "entries")); !os.IsNotExist(err) {
		t.Fatalf("nothing should be written: %v", err)
	}

	root, out, _, _ := newTestRoot(t)
	root.SetArgs([]string{"add", "body", "--tag", "a,B", "--tag", "#c", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Entry entryDTO `json:"entry"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if strings.Join(payload.Entry.Tags, ",") != "a,b,c" {
		t.Fatalf("got tags %v, want a,b,c", payload.Entry.Tags)
	}
}

func TestCLIAddPreservesSuppliedOffset(t *testing.T) {
	root, out, _, data := newTestRoot(t)
	root.SetArgs([]string{"add", "body", "--at", "2026-10-24T14:32:00-04:00", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Entry entryDTO `json:"entry"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Entry.CreatedAt != "2026-10-24T14:32:00-04:00" {
		t.Fatalf("created_at = %s", payload.Entry.CreatedAt)
	}
	file, err := os.ReadFile(filepath.Join(data, filepath.FromSlash(payload.Entry.Path)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(file), "created_at: 2026-10-24T14:32:00-04:00\n") {
		t.Fatalf("file does not keep the supplied offset:\n%s", file)
	}
}

func TestCLIEntryJSONUsesEmptyTagsArray(t *testing.T) {
	root, out, _, _ := newTestRoot(t)
	root.SetArgs([]string{"add", "no tags here", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"tags": []`) {
		t.Fatalf("absent tags must be an empty array:\n%s", out.String())
	}
}

func TestCLIAmbiguousShowListsCandidates(t *testing.T) {
	root, _, _, data := newTestRoot(t)
	for _, id := range []string{"01JB7Q2M8X4T9K3V5N6P0R1S2A", "01JB7Q2M8X4T9K3V5N6P0R1S2B"} {
		writeRawEntry(t, data, "entries/2026/10/"+id+".md",
			"---\nformat: 1\nid: "+id+"\ntype: note\ncreated_at: 2026-10-24T14:32:00-04:00\n---\n\nbody\n")
	}
	root.SetArgs([]string{"show", "01JB7Q2M", "--json"})
	err := root.Execute()
	if errorCode(err) != "ambiguous_id" {
		t.Fatalf("got %v", err)
	}
	for _, id := range []string{"01JB7Q2M8X4T9K3V5N6P0R1S2A", "01JB7Q2M8X4T9K3V5N6P0R1S2B"} {
		if !strings.Contains(err.Error(), id) {
			t.Fatalf("message %q does not list %s", err.Error(), id)
		}
	}
}
