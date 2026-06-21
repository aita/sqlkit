// Command querytrace is a self-contained web server that demonstrates what
// querytrace catches. Every endpoint deliberately runs a different piece of
// "bad code" — an N+1, a missing WHERE, a write where only reads were expected,
// a query budget blown, a leaked cursor, an oversized IN list — and the trace
// for the request is rendered as an HTML page (warnings + timeline), so you can
// see the warning each one raises right in the browser.
//
// It needs no external database: it runs an in-memory SQLite database (the
// pure-Go modernc driver) wrapped by the querytrace driver, and wires the
// PostgreSQL parser for full structural analysis (the SQL here is plain enough
// that SQLite executes it and the PostgreSQL grammar parses it).
//
// Run it and open the index in a browser:
//
//	go run .
//	open http://localhost:8080/
package main

import (
	"bytes"
	"context"
	"database/sql"
	stddriver "database/sql/driver"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"

	querytrace "github.com/aita/sqlkit/querytrace"
	qtdriver "github.com/aita/sqlkit/querytrace/driver"

	// The PostgreSQL parser, wired into the config with WithParser so structural
	// analysis works (the missing-WHERE checks need a real parse, not a keyword
	// guess). The SQL here is plain enough that SQLite executes it and the
	// PostgreSQL grammar parses it.
	"github.com/aita/sqlkit/sqlparse/postgres"

	"modernc.org/sqlite"
)

// demo is one endpoint: its route, a description for the index, the warnings it
// is expected to raise, and the handler that runs the bad code. The handler
// writes its human-readable result to out; the trace is captured separately.
type demo struct {
	Path    string
	Title   string
	Desc    string
	Expect  []string
	Handler func(s *server, out io.Writer, r *http.Request) error
}

// demos is the catalog the index page lists and the router registers.
var demos = []demo{
	{
		Path:    "/users",
		Title:   "Clean baseline",
		Desc:    "One JOIN + GROUP BY query returns every user with their post count.",
		Handler: (*server).demoUsers,
	},
	{
		Path:    "/users/n+1",
		Title:   "N+1 / query in loop",
		Desc:    "List the users (1 query), then run one more query per user to count their posts.",
		Expect:  []string{"query_in_loop", "too_many_queries"},
		Handler: (*server).demoNPlusOne,
	},
	{
		Path:    "/reports",
		Title:   "UPDATE / DELETE without WHERE",
		Desc:    "A table-wide UPDATE and DELETE with no WHERE clause (run in a rolled-back transaction).",
		Expect:  []string{"update_without_where", "delete_without_where"},
		Handler: (*server).demoReports,
	},
	{
		Path:    "/cache/1",
		Title:   "Write under a read-only context",
		Desc:    "The request is marked ReadOnly, then writes anyway — a cache-warm write that slipped into a read path.",
		Expect:  []string{"write_in_read_only"},
		Handler: (*server).demoCache,
	},
	{
		Path:    "/search",
		Title:   "Leaked cursor + query budget",
		Desc:    "Fans out a string of lookups past the per-request budget and leaves a cursor open.",
		Expect:  []string{"too_many_queries", "rows_not_closed"},
		Handler: (*server).demoSearch,
	},
	{
		Path:    "/bulk",
		Title:   "Oversized IN list",
		Desc:    "Builds an IN list large enough to cross the bind-parameter budget.",
		Expect:  []string{"too_many_parameters"},
		Handler: (*server).demoBulk,
	},
}

func main() {
	db, err := openTracedDB()
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close() //nolint:errcheck // best-effort close

	if err := seed(context.Background(), db); err != nil {
		log.Fatal(err)
	}

	srv := &server{db: db, log: slog.New(slog.NewTextHandler(os.Stdout, nil))}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", srv.handleIndex)
	for _, d := range demos {
		mux.HandleFunc("GET "+d.Path, srv.traced(d))
	}

	addr := ":8080"
	log.Printf("listening on %s — open http://localhost%s/ in a browser", addr, addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

// openTracedDB opens an in-memory SQLite database behind the querytrace driver
// wrapper, so every statement run under a request's Trace is recorded.
func openTracedDB() (*sql.DB, error) {
	// Wire the PostgreSQL parser (for structural analysis), turn on
	// development-grade capture (raw SQL + a caller for every statement), and
	// tighten a couple of thresholds so the demo data is enough to trip the
	// warnings.
	cfg := querytrace.NewConfig(
		querytrace.WithParser(postgres.New()),
		querytrace.WithVerboseCapture(),
		querytrace.WithNPlusOneThreshold(3),
		querytrace.WithMaxQueries(8),
		querytrace.WithMaxParams(50),
	)

	connector := &sqliteConnector{dsn: "file::memory:?cache=shared", drv: &sqlite.Driver{}}
	return sql.OpenDB(qtdriver.WrapConnector(connector, cfg)), nil
}

// sqliteConnector adapts the modernc SQLite driver to a driver.Connector so the
// querytrace wrapper has something to wrap.
type sqliteConnector struct {
	dsn string
	drv *sqlite.Driver
}

func (c *sqliteConnector) Connect(context.Context) (stddriver.Conn, error) { return c.drv.Open(c.dsn) }
func (c *sqliteConnector) Driver() stddriver.Driver                        { return c.drv }

type server struct {
	db  *sql.DB
	log *slog.Logger
}

// handleIndex renders the landing page: a card per demo linking to its
// endpoint. With ?format=json (or an application/json Accept header) it returns
// the demo catalog as JSON instead.
func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if wantsJSON(r) {
		metas := make([]demoMeta, len(demos))
		for i, d := range demos {
			metas[i] = d.meta()
		}
		s.writeJSON(w, metas)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := indexTmpl.Execute(w, demos); err != nil {
		s.log.Error("render index", "err", err)
	}
}

// wantsJSON reports whether the request asked for JSON, via a ?format=json query
// parameter or an Accept header that prefers application/json.
func wantsJSON(r *http.Request) bool {
	if r.URL.Query().Get("format") == "json" {
		return true
	}
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

// writeJSON writes v as indented JSON.
func (s *server) writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		s.log.Error("write json", "err", err)
	}
}

// traced wraps a demo's handler so each request runs under its own Trace: it is
// placed in the request context (the driver records every statement against
// it), the handler's output is captured, and the page rendered shows the result
// alongside the trace's warnings and timeline. A summary line is also logged.
func (s *server) traced(d demo) http.HandlerFunc {
	exporter := querytrace.NewLogExporter(s.log)
	return func(w http.ResponseWriter, r *http.Request) {
		trace := querytrace.New("GET " + d.Path)
		ctx := querytrace.WithTrace(r.Context(), trace)

		var out bytes.Buffer
		err := d.Handler(s, &out, r.WithContext(ctx))

		exporter.ExportTrace(ctx, trace)

		errStr := ""
		if err != nil {
			errStr = err.Error()
		}
		rep := trace.Report()

		if wantsJSON(r) {
			s.writeJSON(w, detailJSON{
				Demo:   d.meta(),
				Output: out.String(),
				Error:  errStr,
				Report: rep,
			})
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		view := buildView(d, rep, out.String(), errStr)
		if err := detailTmpl.Execute(w, view); err != nil {
			s.log.Error("render detail", "err", err)
		}
	}
}

// demoUsers is the clean baseline: one JOIN + GROUP BY query returns every user
// with their post count. The trace shows a single query and no warnings.
func (s *server) demoUsers(out io.Writer, r *http.Request) error {
	ctx := r.Context()
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.id, u.name, COUNT(p.id)
		FROM users u
		LEFT JOIN posts p ON p.user_id = u.id
		GROUP BY u.id, u.name
		ORDER BY u.id`)
	if err != nil {
		return err
	}
	defer rows.Close() //nolint:errcheck // best-effort close
	for rows.Next() {
		var id, count int64
		var name string
		if err := rows.Scan(&id, &name, &count); err != nil {
			return err
		}
		fmt.Fprintf(out, "user %d %-7s %d posts\n", id, name, count) //nolint:errcheck // best-effort write to response
	}
	return rows.Err()
}

// demoNPlusOne is the classic N+1: list the users (1 query), then issue one more
// query per user to count their posts. querytrace flags the repeated shape from
// a single call site as a query-in-loop.
func (s *server) demoNPlusOne(out io.Writer, r *http.Request) error {
	ctx := r.Context()
	users, err := s.listUsers(ctx)
	if err != nil {
		return err
	}
	for _, u := range users {
		// This single line runs once per user — the loop that querytrace
		// fingerprints into one repeated shape.
		var count int64
		if err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM posts WHERE user_id = $1`, u.id).Scan(&count); err != nil {
			return err
		}
		fmt.Fprintf(out, "user %d %-7s %d posts\n", u.id, u.name, count) //nolint:errcheck // best-effort write to response
	}
	return nil
}

// demoReports runs an UPDATE and a DELETE with no WHERE clause — the kind of
// statement that quietly rewrites or empties a whole table. It runs them in a
// transaction it rolls back, so the demo data survives, but querytrace still
// records the statements and raises update_without_where / delete_without_where.
func (s *server) demoReports(out io.Writer, r *http.Request) error {
	ctx := r.Context()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // never commit — this is a demonstration of bad SQL

	if _, err := tx.ExecContext(ctx, `UPDATE posts SET published = 1`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM posts`); err != nil {
		return err
	}
	fmt.Fprintln(out, "ran a table-wide UPDATE and DELETE (rolled back)") //nolint:errcheck // best-effort write to response
	return nil
}

// demoCache marks the request context ReadOnly — asserting this path only reads
// — and then writes anyway. querytrace raises write_in_read_only.
func (s *server) demoCache(out io.Writer, r *http.Request) error {
	// The user id is the last path segment (the route is registered as the fixed
	// path, so this is the "1" in /cache/1).
	id, err := strconv.ParseInt(r.URL.Path[strings.LastIndexByte(r.URL.Path, '/')+1:], 10, 64)
	if err != nil {
		return err
	}
	// A read-only scope: a write under it is a contract violation.
	ctx := querytrace.ReadOnly(r.Context())

	var name string
	if err := s.db.QueryRowContext(ctx,
		`SELECT name FROM users WHERE id = $1`, id).Scan(&name); err != nil {
		return err
	}
	// Oops: a "cache warm" write slipped into a read-only path.
	if _, err := s.db.ExecContext(ctx,
		`UPDATE users SET email = email WHERE id = $1`, id); err != nil {
		return err
	}
	fmt.Fprintf(out, "loaded user %d %q (and wrote under a read-only context)\n", id, name) //nolint:errcheck // best-effort write to response
	return nil
}

// demoSearch leaks a cursor (opens rows and returns without closing them) and
// fans out enough queries to blow the per-request budget. querytrace raises
// rows_not_closed and too_many_queries.
func (s *server) demoSearch(out io.Writer, r *http.Request) error {
	ctx := r.Context()

	// Blow the query budget (MaxQueries = 8): run a string of distinct lookups.
	for i := int64(1); i <= 10; i++ {
		var name string
		// Ignore ErrNoRows: the point is the number of statements, not the rows.
		_ = s.db.QueryRowContext(ctx, `SELECT name FROM users WHERE id = $1`, i).Scan(&name)
	}

	// Leak a cursor: open it, read one row, and return without Close().
	rows, err := s.db.QueryContext(ctx, `SELECT id, name FROM users ORDER BY id`)
	if err != nil {
		return err
	}
	if rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return err
		}
		fmt.Fprintf(out, "first user: %d %s\n", id, name) //nolint:errcheck // best-effort write to response
	}
	// Intentionally NOT calling rows.Close().
	fmt.Fprintln(out, "ran a fan-out of lookups and leaked a cursor") //nolint:errcheck // best-effort write to response
	return nil
}

// demoBulk builds an IN list large enough to cross the parameter budget
// (MaxParams = 50). querytrace raises too_many_parameters — the signature of an
// oversized IN list or batch that should have been chunked.
func (s *server) demoBulk(out io.Writer, r *http.Request) error {
	ctx := r.Context()

	const n = 60
	placeholders := make([]string, n)
	args := make([]any, n)
	for i := 0; i < n; i++ {
		placeholders[i] = "$" + strconv.Itoa(i+1)
		args[i] = i + 1
	}
	query := `SELECT COUNT(*) FROM users WHERE id IN (` + strings.Join(placeholders, ", ") + `)`

	var count int64
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return err
	}
	fmt.Fprintf(out, "matched %d users with a %d-parameter IN list\n", count, n) //nolint:errcheck // best-effort write to response
	return nil
}

type user struct {
	id   int64
	name string
}

func (s *server) listUsers(ctx context.Context) ([]user, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // best-effort close
	var users []user
	for rows.Next() {
		var u user
		if err := rows.Scan(&u.id, &u.name); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// seed creates the schema and a little data. It runs without a Trace in the
// context, so none of it is recorded against a request.
func seed(ctx context.Context, db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL, email TEXT NOT NULL)`,
		`CREATE TABLE posts (id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL, title TEXT NOT NULL, published INTEGER NOT NULL DEFAULT 0)`,
	}
	for _, q := range stmts {
		if _, err := db.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	names := []string{"alice", "bob", "carol", "dave", "erin", "frank", "grace", "heidi"}
	for i, name := range names {
		id := i + 1
		if _, err := db.ExecContext(ctx,
			`INSERT INTO users (id, name, email) VALUES ($1, $2, $3)`,
			id, name, name+"@example.com"); err != nil {
			return err
		}
		for p := 0; p <= i%3; p++ {
			if _, err := db.ExecContext(ctx,
				`INSERT INTO posts (user_id, title) VALUES ($1, $2)`,
				id, fmt.Sprintf("%s post %d", name, p+1)); err != nil {
				return err
			}
		}
	}
	return nil
}
