// Command fts5-spike checks that modernc.org/sqlite supports what D-009 needs.
package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	_ "modernc.org/sqlite"
)

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL:", err)
		os.Exit(1)
	}
}

func run(w *os.File) error {
	fmt.Fprintf(w, "platform: %s/%s, go: %s\n", runtime.GOOS, runtime.GOARCH, runtime.Version())

	dir, err := os.MkdirTemp("", "fts5-spike-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	// 1+2: driver imported (blank import above) and a file database opens.
	db, err := sql.Open("sqlite", filepath.Join(dir, "index.db"))
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return fmt.Errorf("ping: %w", err)
	}
	var version string
	if err := db.QueryRow("SELECT sqlite_version()").Scan(&version); err != nil {
		return err
	}
	fmt.Fprintf(w, "PASS 1-2 open database (SQLite %s)\n", version)

	var journal string
	if err := db.QueryRow("PRAGMA journal_mode=WAL").Scan(&journal); err != nil {
		return fmt.Errorf("wal: %w", err)
	}
	fmt.Fprintf(w, "INFO journal_mode=%s\n", journal)

	// 3: FTS5 compiled in.
	var fts5 int
	if err := db.QueryRow("SELECT sqlite_compileoption_used('ENABLE_FTS5')").Scan(&fts5); err != nil {
		return err
	}
	if fts5 != 1 {
		return fmt.Errorf("ENABLE_FTS5 = %d", fts5)
	}
	fmt.Fprintln(w, "PASS 3 ENABLE_FTS5 = 1")

	// 4: virtual table.
	if _, err := db.Exec(`CREATE VIRTUAL TABLE entries_fts USING fts5(entry_id UNINDEXED, title, body, tokenize='unicode61')`); err != nil {
		return fmt.Errorf("create fts5 table: %w", err)
	}
	fmt.Fprintln(w, "PASS 4 created FTS5 virtual table")

	// 5: insert, with bound parameters only.
	docs := [][3]string{
		{"01JB7Q2M8X4T9K3V5N6P0R1S2A", "Token refresh race in API client", "Two requests refreshed the token at once. Fix: single-flight refresh."},
		{"01JB7Q2M8X4T9K3V5N6P0R1S2B", "Docker build cache", "Use BuildKit cache mounts for go mod download."},
		{"01JB7Q2M8X4T9K3V5N6P0R1S2C", "Café notes", "Unicode check: naïve résumé"},
	}
	for _, d := range docs {
		if _, err := db.Exec(`INSERT INTO entries_fts(entry_id, title, body) VALUES (?, ?, ?)`, d[0], d[1], d[2]); err != nil {
			return fmt.Errorf("insert: %w", err)
		}
	}
	fmt.Fprintf(w, "PASS 5 inserted %d documents\n", len(docs))

	// 6: searches return the expected IDs.
	cases := []struct{ query, want string }{
		{`token refresh`, docs[0][0]},
		{`"single-flight"`, docs[0][0]},
		{`build*`, docs[1][0]},
		{`resume`, docs[2][0]}, // unicode61 removes diacritics by default
	}
	for _, c := range cases {
		rows, err := db.Query(`SELECT entry_id, snippet(entries_fts, 2, '[', ']', '…', 6) FROM entries_fts WHERE entries_fts MATCH ? ORDER BY bm25(entries_fts)`, c.query)
		if err != nil {
			return fmt.Errorf("search %q: %w", c.query, err)
		}
		var got []string
		var snip string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id, &snip); err != nil {
				rows.Close()
				return err
			}
			got = append(got, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(got) != 1 || got[0] != c.want {
			return fmt.Errorf("search %q: got %v, want [%s]", c.query, got, c.want)
		}
		fmt.Fprintf(w, "PASS 6 search %-16s -> %s  snippet: %s\n", c.query, got[0][:8], snip)
	}

	var none int
	if err := db.QueryRow(`SELECT count(*) FROM entries_fts WHERE entries_fts MATCH ?`, "kubernetes").Scan(&none); err != nil {
		return err
	}
	if none != 0 {
		return fmt.Errorf("non-matching search returned %d rows", none)
	}
	fmt.Fprintln(w, "PASS 6 non-matching search returns 0 rows")

	// Stemming: compare porter against plain unicode61 on the same text.
	if _, err := db.Exec(`CREATE VIRTUAL TABLE stem_fts USING fts5(body, tokenize='porter unicode61')`); err != nil {
		return fmt.Errorf("porter tokenizer: %w", err)
	}
	if _, err := db.Exec(`INSERT INTO stem_fts(body) VALUES (?)`, "The client refreshed tokens twice"); err != nil {
		return err
	}
	var stemmed, plain int
	if err := db.QueryRow(`SELECT count(*) FROM stem_fts WHERE stem_fts MATCH ?`, "refreshing").Scan(&stemmed); err != nil {
		return err
	}
	if err := db.QueryRow(`SELECT count(*) FROM entries_fts WHERE entries_fts MATCH ?`, "refreshing").Scan(&plain); err != nil {
		return err
	}
	if stemmed != 1 {
		return fmt.Errorf("porter: 'refreshing' matched %d rows, want 1", stemmed)
	}
	fmt.Fprintf(w, "PASS porter: 'refreshing' finds 'refreshed' (%d hit); plain unicode61: %d hits\n", stemmed, plain)
	fmt.Fprintln(w, "ALL CHECKS PASSED")
	return nil
}
