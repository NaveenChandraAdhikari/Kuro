package storage

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/oklog/ulid/v2"
	"kuro/internal/domain"
)

const (
	FormatVersion = 1
	MaxFileSize   = 10 * 1024 * 1024
	WarnFileSize  = 1 * 1024 * 1024
)

func SerializedSize(e domain.Entry) (int, error) {
	data, err := Serialize(e)
	if err != nil {
		return 0, err
	}
	return len(data), nil
}

type ParseError struct {
	Code    string
	Line    int
	Message string
}

func (e *ParseError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("line %d: %s", e.Line, e.Message)
	}
	return e.Message
}

func parseError(code string, line int, format string, args ...any) error {
	return &ParseError{Code: code, Line: line, Message: fmt.Sprintf(format, args...)}
}

func Serialize(e domain.Entry) ([]byte, error) {
	if e.Format == 0 {
		e.Format = FormatVersion
	}
	if e.Format != FormatVersion {
		return nil, fmt.Errorf("unsupported format %d", e.Format)
	}
	if e.UpdatedAt.IsZero() {
		e.UpdatedAt = e.CreatedAt
	}
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "format: %d\n", FormatVersion)
	fmt.Fprintf(&b, "id: %s\n", strings.ToUpper(e.ID))
	fmt.Fprintf(&b, "type: %s\n", e.Type)
	if e.Title != "" {
		fmt.Fprintf(&b, "title: %s\n", quoteScalar(e.Title))
	}
	fmt.Fprintf(&b, "created_at: %s\n", e.CreatedAt.Format(time.RFC3339Nano))
	fmt.Fprintf(&b, "updated_at: %s\n", e.UpdatedAt.Format(time.RFC3339Nano))
	if e.Project != "" {
		fmt.Fprintf(&b, "project: %s\n", quoteScalar(e.Project))
	}
	if len(e.Tags) > 0 {
		tags := make([]string, len(e.Tags))
		for i, tag := range e.Tags {
			tags[i] = quoteScalar(tag)
		}
		fmt.Fprintf(&b, "tags: [%s]\n", strings.Join(tags, ", "))
	}
	if e.Status != "" {
		fmt.Fprintf(&b, "status: %s\n", e.Status)
	}
	if e.Priority != "" {
		fmt.Fprintf(&b, "priority: %s\n", e.Priority)
	}
	if e.CompletedAt != nil {
		fmt.Fprintf(&b, "completed_at: %s\n", e.CompletedAt.Format(time.RFC3339Nano))
	}
	if len(e.Links) > 0 {
		fmt.Fprintf(&b, "links: [%s]\n", strings.Join(e.Links, ", "))
	}
	if e.Source != "" {
		fmt.Fprintf(&b, "source: %s\n", e.Source)
	}
	if e.GitRepo != "" {
		fmt.Fprintf(&b, "git_repo: %s\n", quoteScalar(e.GitRepo))
	}
	if e.GitBranch != "" {
		fmt.Fprintf(&b, "git_branch: %s\n", quoteScalar(e.GitBranch))
	}
	if e.GitCommit != "" {
		fmt.Fprintf(&b, "git_commit: %s\n", quoteScalar(e.GitCommit))
	}
	b.WriteString("---\n")
	b.WriteString("\n")
	b.WriteString(e.Body)
	return []byte(b.String()), nil
}

type sourceLine struct {
	Text string
	Line int
}

func Parse(data []byte) (domain.Entry, error) {
	if len(data) > MaxFileSize {
		return domain.Entry{}, parseError("oversized", 0, "file exceeds %d bytes", MaxFileSize)
	}
	if !utf8.Valid(data) {
		return domain.Entry{}, parseError("invalid_utf8", 0, "file is not valid UTF-8")
	}
	if bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) {
		data = data[3:]
	}
	lines, bodyStart, err := frontMatterLines(data)
	if err != nil {
		return domain.Entry{}, err
	}
	values := make(map[string]string)
	lists := make(map[string][]string)
	seen := make(map[string]bool)
	keyLines := make(map[string]int)
	var currentList string
	frontMatterEnd := len(lines) - 1
	for _, line := range lines[1:frontMatterEnd] {
		text := line.Text
		if strings.TrimSpace(text) == "" || strings.HasPrefix(strings.TrimSpace(text), "#") {
			continue
		}
		if strings.HasPrefix(text, "  - ") {
			if currentList == "" {
				return domain.Entry{}, parseError("invalid_front_matter", line.Line, "list item without a list key")
			}
			item, err := parseScalar(strings.TrimSpace(strings.TrimPrefix(text, "  - ")), line.Line)
			if err != nil {
				return domain.Entry{}, err
			}
			lists[currentList] = append(lists[currentList], item)
			continue
		}
		currentList = ""
		key, raw, ok := strings.Cut(text, ":")
		if !ok || !validKey(strings.TrimSpace(key)) {
			return domain.Entry{}, parseError("invalid_front_matter", line.Line, "invalid front-matter line %q", text)
		}
		key = strings.TrimSpace(key)
		if seen[key] {
			return domain.Entry{}, parseError("duplicate_key", line.Line, "duplicate front-matter key %q", key)
		}
		seen[key] = true
		keyLines[key] = line.Line
		raw = strings.TrimSpace(raw)
		if raw == "" {
			currentList = key
			values[key] = ""
			continue
		}
		if strings.HasPrefix(raw, "[") {
			items, err := parseFlowList(raw, line.Line)
			if err != nil {
				return domain.Entry{}, err
			}
			lists[key] = items
			values[key] = raw
			continue
		}
		value, err := parseScalarForKey(raw, key, line.Line)
		if err != nil {
			return domain.Entry{}, err
		}
		values[key] = value
	}

	formatText, ok := values["format"]
	if !ok || formatText == "" {
		return domain.Entry{}, parseError("missing_required", 0, "missing required key %q", "format")
	}
	format, err := strconv.Atoi(formatText)
	if err != nil || strconv.Itoa(format) != formatText || format != FormatVersion {
		return domain.Entry{}, parseError("unsupported_format", keyLines["format"], "unsupported format %q", formatText)
	}
	idText, ok := values["id"]
	if !ok || idText == "" {
		return domain.Entry{}, parseError("missing_required", 0, "missing required key %q", "id")
	}
	id, err := ulid.ParseStrict(strings.ToUpper(idText))
	if err != nil {
		return domain.Entry{}, parseError("invalid_id", keyLines["id"], "invalid ULID %q", idText)
	}
	typeText, ok := values["type"]
	if !ok || typeText == "" {
		return domain.Entry{}, parseError("missing_required", 0, "missing required key %q", "type")
	}
	createdText, ok := values["created_at"]
	if !ok || createdText == "" {
		return domain.Entry{}, parseError("missing_required", 0, "missing required key %q", "created_at")
	}
	created, err := time.Parse(time.RFC3339Nano, createdText)
	if err != nil {
		return domain.Entry{}, parseError("invalid_timestamp", keyLines["created_at"], "invalid created_at: %v", err)
	}
	updated := created
	if updatedText := values["updated_at"]; updatedText != "" {
		updated, err = time.Parse(time.RFC3339Nano, updatedText)
		if err != nil {
			return domain.Entry{}, parseError("invalid_timestamp", keyLines["updated_at"], "invalid updated_at: %v", err)
		}
	}
	entry := domain.Entry{
		ID:        id.String(),
		Format:    format,
		Type:      typeText,
		Title:     values["title"],
		Body:      string(data[bodyStart:]),
		CreatedAt: created,
		UpdatedAt: updated,
		Project:   domain.NormalizeProject(values["project"]),
		Tags:      domain.NormalizeTags(lists["tags"]),
		Status:    values["status"],
		Priority:  values["priority"],
		Source:    values["source"],
		GitRepo:   values["git_repo"],
		GitBranch: values["git_branch"],
		GitCommit: values["git_commit"],
	}
	if completed := values["completed_at"]; completed != "" {
		value, err := time.Parse(time.RFC3339Nano, completed)
		if err != nil {
			return domain.Entry{}, parseError("invalid_timestamp", keyLines["completed_at"], "invalid completed_at: %v", err)
		}
		entry.CompletedAt = &value
	}
	for _, link := range lists["links"] {
		linkID, err := ulid.ParseStrict(strings.ToUpper(link))
		if err != nil {
			return domain.Entry{}, parseError("invalid_links", keyLines["links"], "invalid link %q", link)
		}
		entry.Links = append(entry.Links, linkID.String())
	}
	if err := domain.ValidateRead(entry); err != nil {
		return domain.Entry{}, parseError("validation_failed", readErrorLine(err, keyLines, data), "%v", err)
	}
	return entry, nil
}

// readErrorLine maps a validation error to the line of the key it concerns,
// or 0 when the problem applies to the whole file.
func readErrorLine(err error, keyLines map[string]int, data []byte) int {
	keys := []struct {
		target error
		key    string
	}{
		{domain.ErrInvalidType, "type"},
		{domain.ErrInvalidTitle, "title"},
		{domain.ErrInvalidProject, "project"},
		{domain.ErrInvalidTag, "tags"},
		{domain.ErrInvalidStatus, "status"},
		{domain.ErrMissingStatus, "type"},
		{domain.ErrMissingTitle, "type"},
		{domain.ErrInvalidPriority, "priority"},
		{domain.ErrInvalidSource, "source"},
	}
	for _, item := range keys {
		if errors.Is(err, item.target) {
			return keyLines[item.key]
		}
	}
	if errors.Is(err, domain.ErrInvalidBody) {
		if index := bytes.IndexByte(data, 0); index >= 0 {
			return bytes.Count(data[:index], []byte("\n")) + 1
		}
	}
	return 0
}

func frontMatterLines(data []byte) ([]sourceLine, int, error) {
	lines := make([]sourceLine, 0)
	start := 0
	lineNumber := 1
	closingEnd := -1
	for start < len(data) {
		end := bytes.IndexByte(data[start:], '\n')
		lineEnd := len(data)
		if end >= 0 {
			lineEnd = start + end
		}
		text := string(data[start:lineEnd])
		text = strings.TrimSuffix(text, "\r")
		lines = append(lines, sourceLine{Text: text, Line: lineNumber})
		if lineNumber > 1 && text == "---" {
			closingEnd = lineEnd
			if end >= 0 {
				closingEnd++
			}
			break
		}
		if end < 0 {
			break
		}
		start = lineEnd + 1
		lineNumber++
	}
	if len(lines) == 0 || lines[0].Text != "---" {
		return nil, 0, parseError("invalid_front_matter", 1, "missing opening front-matter delimiter")
	}
	if closingEnd < 0 {
		return nil, 0, parseError("invalid_front_matter", 0, "missing closing front-matter delimiter")
	}
	bodyStart := closingEnd
	if bodyStart < len(data) {
		if data[bodyStart] == '\r' && bodyStart+1 < len(data) && data[bodyStart+1] == '\n' {
			bodyStart += 2
		} else if data[bodyStart] == '\n' {
			bodyStart++
		}
	}
	return lines, bodyStart, nil
}

func validKey(key string) bool {
	if key == "" || key[0] < 'a' || key[0] > 'z' {
		return false
	}
	for _, r := range key[1:] {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_' {
			return false
		}
	}
	return true
}

func parseScalar(raw string, line int) (string, error) {
	return parseScalarForKey(raw, "", line)
}

func parseScalarForKey(raw, key string, line int) (string, error) {
	if raw == "" {
		return "", nil
	}
	if raw[0] != '"' {
		if !allowsUnquotedSpecials(key) && strings.ContainsAny(raw, ":#[],") {
			return "", parseError("invalid_scalar", line, "value containing : # [ ] or , must be quoted")
		}
		return raw, nil
	}
	if len(raw) < 2 || raw[len(raw)-1] != '"' {
		return "", parseError("invalid_scalar", line, "unterminated quoted scalar")
	}
	var b strings.Builder
	for i := 1; i < len(raw)-1; i++ {
		if raw[i] == '\\' {
			if i+1 >= len(raw)-1 || (raw[i+1] != '\\' && raw[i+1] != '"') {
				return "", parseError("invalid_scalar", line, "only \\\" and \\\\ escapes are supported")
			}
			i++
		}
		b.WriteByte(raw[i])
	}
	return b.String(), nil
}

func allowsUnquotedSpecials(key string) bool {
	switch key {
	case "created_at", "updated_at", "completed_at":
		return true
	default:
		return false
	}
}

func parseFlowList(raw string, line int) ([]string, error) {
	if len(raw) < 2 || raw[len(raw)-1] != ']' {
		return nil, parseError("invalid_list", line, "unterminated flow list")
	}
	content := strings.TrimSpace(raw[1 : len(raw)-1])
	if content == "" {
		return nil, nil
	}
	var parts []string
	start := 0
	inQuote := false
	escaped := false
	for i, r := range content {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && inQuote {
			escaped = true
			continue
		}
		if r == '"' {
			inQuote = !inQuote
		}
		if r == ',' && !inQuote {
			parts = append(parts, content[start:i])
			start = i + 1
		}
	}
	if inQuote {
		return nil, parseError("invalid_list", line, "unterminated quoted list item")
	}
	parts = append(parts, content[start:])
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		item, err := parseScalar(strings.TrimSpace(part), line)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func quoteScalar(value string) string {
	if strings.ContainsAny(value, ":#[],\"") || strings.TrimSpace(value) != value {
		return `"` + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`) + `"`
	}
	return value
}
