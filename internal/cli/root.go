package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
	"unicode"

	"charm.land/fang/v2"
	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"
	"kuro/internal/app"
	"kuro/internal/domain"
	"kuro/internal/storage"
)

var (
	Version = "0.1.0-dev"
	Commit  = "unknown"
)

type Root struct {
	Paths storage.Paths
	App   *app.Service
	JSON  bool
}

type entryDTO struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"`
	Title       string   `json:"title,omitempty"`
	Body        string   `json:"body"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
	Project     string   `json:"project,omitempty"`
	Tags        []string `json:"tags"`
	Status      string   `json:"status,omitempty"`
	Priority    string   `json:"priority,omitempty"`
	CompletedAt *string  `json:"completed_at,omitempty"`
	Links       []string `json:"links,omitempty"`
	Source      string   `json:"source,omitempty"`
	Path        string   `json:"path,omitempty"`
}

type collectionDTO struct {
	SchemaVersion int        `json:"schema_version"`
	Entries       []entryDTO `json:"entries"`
	Errors        []issueDTO `json:"errors"`
	Warnings      []issueDTO `json:"warnings"`
}

// issueDTO is the per-file problem shape from design §9.2.
type issueDTO struct {
	Code    string   `json:"code"`
	Path    string   `json:"path"`
	Paths   []string `json:"paths,omitempty"`
	Line    int      `json:"line"`
	Message string   `json:"message"`
}

type fatalDTO struct {
	SchemaVersion int `json:"schema_version"`
	Error         struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func newRoot() (*cobra.Command, *Root, error) {
	paths, err := storage.DefaultPaths()
	if err != nil {
		return nil, nil, err
	}
	state := &Root{Paths: paths}
	state.App = &app.Service{Repo: app.NewFileRepository(paths)}
	root := &cobra.Command{
		Use:           "kuro",
		Short:         "Local-first developer memory",
		SilenceUsage:  true,
		SilenceErrors: true,
		// Setting Args makes an unknown subcommand reach RunE instead of a
		// generic cobra error, so it can be reported as a usage error.
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return &storage.ResolveError{Code: "usage", Message: fmt.Sprintf("unknown command %q for %q", args[0], cmd.CommandPath())}
			}
			return cmd.Help()
		},
	}
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &storage.ResolveError{Code: "usage", Message: err.Error()}
	})
	root.PersistentFlags().BoolVar(&state.JSON, "json", false, "write machine-readable JSON")
	root.AddCommand(addCommand(state), listCommand(state), showCommand(state), statusCommand(state), versionCommand(state))
	return root, state, nil
}

func NewRoot() (*cobra.Command, error) {
	root, _, err := newRoot()
	return root, err
}

func Execute(ctx context.Context) error {
	root, state, err := newRoot()
	if err != nil {
		return err
	}
	// Flag parsing can fail before --json is parsed, so the error handler also
	// checks the raw arguments.
	return fang.Execute(ctx, root, fang.WithVersion(Version), fang.WithErrorHandler(errorHandler(state, jsonRequested(os.Args[1:]))))
}

// jsonRequested reports whether --json appears before any "--" separator.
func jsonRequested(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "--":
			return false
		case "--json", "--json=true":
			return true
		}
	}
	return false
}

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var resolveErr *storage.ResolveError
	if errors.As(err, &resolveErr) && resolveErr.Code == "usage" {
		return 2
	}
	return 1
}

func addCommand(state *Root) *cobra.Command {
	var title, entryType, project, at string
	var tagValues []string
	cmd := &cobra.Command{
		Use:   "add [body]",
		Short: "Save a developer note",
		Args:  maxOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			body := ""
			if len(args) == 1 {
				body = args[0]
			}
			var timestamp *time.Time
			if at != "" {
				parsed, err := time.Parse(time.RFC3339Nano, at)
				if err != nil {
					return &storage.ResolveError{Code: "validation_failed", Message: fmt.Sprintf("invalid --at timestamp: %v", err)}
				}
				timestamp = &parsed
			}
			added, err := state.App.Add(title, body, entryType, project, splitTags(tagValues), timestamp)
			if err != nil {
				return err
			}
			if size, err := storage.SerializedSize(added.Entry); err == nil && size > storage.WarnFileSize {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: entry is %d bytes; files above %d bytes may be inconvenient to edit\n", size, storage.WarnFileSize)
			}
			for _, warning := range added.Warnings {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", warning)
			}
			return printEntryCreated(cmd, state, added)
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "note title")
	cmd.Flags().StringVar(&entryType, "type", domain.TypeNote, "entry type: note or journal")
	cmd.Flags().StringVar(&project, "project", "", "project slug")
	// StringArray keeps each value intact; StringSlice would parse it as CSV
	// and silently drop text after a newline.
	cmd.Flags().StringArrayVar(&tagValues, "tag", nil, "tag; repeat or comma-separate")
	cmd.Flags().StringVar(&at, "at", "", "RFC3339 timestamp with offset")
	return cmd
}

func listCommand(state *Root) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List saved notes",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := state.App.List()
			if err != nil {
				return err
			}
			if state.JSON {
				return printJSON(cmd, collectionDTOFor(result))
			}
			for _, entry := range result.Entries {
				fmt.Fprintf(cmd.OutOrStdout(), "%s  %-9s %s\n", displayID(entry.ID, result.Entries), entry.Type, entryTitle(entry))
			}
			if len(result.Entries) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no entries yet")
			}
			printIssues(cmd, result.Issues, 5)
			return nil
		},
	}
}

func showCommand(state *Root) *cobra.Command {
	return &cobra.Command{
		Use:   "show <id-or-prefix>",
		Short: "Show one note",
		Args:  exactOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			entry, result, err := state.App.Show(args[0])
			if err != nil {
				return err
			}
			if state.JSON {
				return printJSON(cmd, map[string]any{"schema_version": 1, "entry": entryDTOFor(entry, result.EntryPaths[entry.ID])})
			}
			return printEntry(cmd, entry)
		},
	}
}

func statusCommand(state *Root) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show Kuro paths and collection status",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := state.App.List()
			if err != nil {
				return err
			}
			errorCount, warningCount := issueCounts(result.Issues)
			if state.JSON {
				return printJSON(cmd, map[string]any{
					"schema_version": 1,
					"paths":          map[string]string{"data": state.Paths.Data, "config": state.Paths.Config, "cache": state.Paths.Cache},
					"entry_count":    len(result.Entries), "error_count": errorCount, "warning_count": warningCount,
				})
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "data: %s\nconfig: %s\ncache: %s\nentry_count: %d\nerror_count: %d\nwarning_count: %d\n", state.Paths.Data, state.Paths.Config, state.Paths.Cache, len(result.Entries), errorCount, warningCount)
			if errorCount > 0 || warningCount > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "run `kuro list` for file details")
			}
			return nil
		},
	}
}

func versionCommand(state *Root) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show Kuro version",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			version, commit, goVersion := buildInfo()
			if state.JSON {
				return printJSON(cmd, map[string]any{"schema_version": 1, "version": version, "commit": commit, "go_version": goVersion})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "version: %s\ncommit: %s\ngo_version: %s\n", version, commit, goVersion)
			return nil
		},
	}
}

func buildInfo() (string, string, string) {
	version, commit, goVersion := Version, Commit, runtime.Version()
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				if commit == "unknown" {
					commit = setting.Value
				}
			case "vcs.modified":
				if setting.Value == "true" && commit != "unknown" {
					commit += "-dirty"
				}
			}
		}
	}
	return version, commit, goVersion
}

// splitTags expands comma-separated --tag values. Each part keeps any other
// characters, so validation can reject them instead of losing them.
func splitTags(values []string) []string {
	var tags []string
	for _, value := range values {
		tags = append(tags, strings.Split(value, ",")...)
	}
	return tags
}

func printEntryCreated(cmd *cobra.Command, state *Root, added app.AddResult) error {
	entry := added.Entry
	if state.JSON {
		return printJSON(cmd, map[string]any{"schema_version": 1, "entry": entryDTOFor(entry, added.Path)})
	}
	message := fmt.Sprintf("saved %s", displayID(entry.ID, []domain.Entry{entry}))
	if !isStyledOutput(cmd) {
		fmt.Fprintln(cmd.OutOrStdout(), message)
		return nil
	}
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	fmt.Fprintln(cmd.OutOrStdout(), style.Render(message))
	return nil
}

func printEntry(cmd *cobra.Command, entry domain.Entry) error {
	out := cmd.OutOrStdout()
	if entry.Title != "" {
		fmt.Fprintln(out, sanitizeSingleLine(entry.Title))
	}
	fmt.Fprintf(out, "id: %s\ntype: %s\ncreated_at: %s\nupdated_at: %s\n", entry.ID, entry.Type, entry.CreatedAt.Format(time.RFC3339Nano), entry.UpdatedAt.Format(time.RFC3339Nano))
	if entry.Project != "" {
		fmt.Fprintf(out, "project: %s\n", sanitizeSingleLine(entry.Project))
	}
	if len(entry.Tags) > 0 {
		fmt.Fprintf(out, "tags: %s\n", sanitizeSingleLine(strings.Join(entry.Tags, ", ")))
	}
	fmt.Fprintln(out, "---")
	if entry.Body != "" {
		fmt.Fprintln(out, sanitizeBody(entry.Body))
	}
	return nil
}

func printJSON(cmd *cobra.Command, value any) error {
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func collectionDTOFor(result app.Collection) collectionDTO {
	errorsList, warnings := splitIssues(result.Issues)
	entries := make([]entryDTO, 0, len(result.Entries))
	for _, entry := range result.Entries {
		entries = append(entries, entryDTOFor(entry, result.EntryPaths[entry.ID]))
	}
	return collectionDTO{SchemaVersion: 1, Entries: entries, Errors: issueDTOs(errorsList), Warnings: issueDTOs(warnings)}
}

func issueDTOs(issues []app.Issue) []issueDTO {
	result := make([]issueDTO, 0, len(issues))
	for _, issue := range issues {
		result = append(result, issueDTO{Code: contractIssueCode(issue.Code), Path: issue.Path, Paths: issue.Paths, Line: issue.Line, Message: issue.Message})
	}
	return result
}

// contractIssueCode maps storage's detailed codes onto the per-file codes
// approved in design §9.2. The detailed reason stays in the message.
func contractIssueCode(code string) string {
	switch code {
	case "unsupported_format", "duplicate_id", "filename_mismatch", "filename_not_ulid":
		return code
	default:
		return "parse_error"
	}
}

func entryDTOFor(entry domain.Entry, path string) entryDTO {
	var completedAt *string
	if entry.CompletedAt != nil {
		value := entry.CompletedAt.Format(time.RFC3339Nano)
		completedAt = &value
	}
	return entryDTO{ID: entry.ID, Type: entry.Type, Title: entry.Title, Body: entry.Body, CreatedAt: entry.CreatedAt.Format(time.RFC3339Nano), UpdatedAt: entry.UpdatedAt.Format(time.RFC3339Nano), Project: entry.Project, Tags: append([]string{}, entry.Tags...), Status: entry.Status, Priority: entry.Priority, CompletedAt: completedAt, Links: append([]string(nil), entry.Links...), Source: entry.Source, Path: path}
}

func printIssues(cmd *cobra.Command, issues []app.Issue, limit int) {
	if len(issues) == 0 {
		return
	}
	errorsList, warnings := splitIssues(issues)
	if len(errorsList) > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "%d files could not be read:\n", len(errorsList))
		for i, issue := range errorsList {
			if i == limit {
				break
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "  %s\n", formatIssue(issue))
		}
	}
	if len(warnings) > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "%d file warnings:\n", len(warnings))
		for i, issue := range warnings {
			if i == limit {
				break
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "  %s\n", formatIssue(issue))
		}
	}
}

func splitIssues(issues []app.Issue) ([]app.Issue, []app.Issue) {
	var errs, warnings []app.Issue
	for _, issue := range issues {
		if issue.Warning {
			warnings = append(warnings, issue)
		} else {
			errs = append(errs, issue)
		}
	}
	return errs, warnings
}

func issueCounts(issues []app.Issue) (int, int) {
	errs, warnings := splitIssues(issues)
	return len(errs), len(warnings)
}

func formatIssue(issue app.Issue) string {
	line := ""
	if issue.Line > 0 {
		line = fmt.Sprintf(":%d", issue.Line)
	}
	return fmt.Sprintf("%s%s: %s", issue.Path, line, issue.Message)
}

func entryTitle(entry domain.Entry) string {
	if entry.Title != "" {
		return sanitizeSingleLine(entry.Title)
	}
	line := strings.Split(strings.TrimSpace(entry.Body), "\n")[0]
	line = sanitizeSingleLine(line)
	if len([]rune(line)) > 72 {
		return string([]rune(line)[:72]) + "…"
	}
	return line
}

func displayID(id string, entries []domain.Entry) string {
	for length := 8; length < len(id); length++ {
		prefix := id[:length]
		count := 0
		for _, entry := range entries {
			if strings.HasPrefix(entry.ID, prefix) {
				count++
			}
		}
		if count == 1 {
			return prefix
		}
	}
	return id
}

func sanitizeSingleLine(value string) string {
	return sanitize(value, false)
}

func sanitizeBody(value string) string { return sanitize(value, true) }

func sanitize(value string, allowNewlineTab bool) string {
	var b strings.Builder
	ansi := false
	for _, r := range value {
		if ansi {
			if r >= '@' && r <= '~' {
				ansi = false
			}
			continue
		}
		if r == '\x1b' {
			ansi = true
			continue
		}
		if unicode.IsControl(r) || (r >= 0x80 && r <= 0x9f) {
			if allowNewlineTab && (r == '\n' || r == '\t') {
				b.WriteRune(r)
			} else {
				b.WriteRune(' ')
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isStyledOutput(cmd *cobra.Command) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	file, ok := cmd.OutOrStdout().(*os.File)
	if !ok || file == nil {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func errorHandler(state *Root, jsonArg bool) fang.ErrorHandler {
	return func(w io.Writer, styles fang.Styles, err error) {
		if state.JSON || jsonArg {
			payload := fatalDTO{SchemaVersion: 1}
			payload.Error.Code, payload.Error.Message = errorCode(err), err.Error()
			encoder := json.NewEncoder(w)
			_ = encoder.Encode(payload)
			return
		}
		fang.DefaultErrorHandler(w, styles, err)
	}
}

func errorCode(err error) string {
	var resolveErr *storage.ResolveError
	if errors.As(err, &resolveErr) && resolveErr.Code != "" {
		return resolveErr.Code
	}
	if errors.Is(err, domain.ErrEmptyEntry) || errors.Is(err, domain.ErrInvalidType) || errors.Is(err, domain.ErrInvalidProject) || errors.Is(err, domain.ErrInvalidTag) || errors.Is(err, domain.ErrInvalidTitle) || errors.Is(err, domain.ErrInvalidBody) || errors.Is(err, domain.ErrFutureTimestamp) {
		return "validation_failed"
	}
	return "io_error"
}

func noArgs(cmd *cobra.Command, args []string) error {
	if len(args) != 0 {
		return &storage.ResolveError{Code: "usage", Message: fmt.Sprintf("%s accepts no arguments", cmd.CommandPath())}
	}
	return nil
}

func maxOneArg(cmd *cobra.Command, args []string) error {
	if len(args) > 1 {
		return &storage.ResolveError{Code: "usage", Message: fmt.Sprintf("%s accepts at most one argument", cmd.CommandPath())}
	}
	return nil
}

func exactOneArg(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return &storage.ResolveError{Code: "usage", Message: fmt.Sprintf("%s requires exactly one argument", cmd.CommandPath())}
	}
	return nil
}
