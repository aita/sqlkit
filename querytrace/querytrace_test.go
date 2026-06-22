package querytrace_test

import (
	"context"
	"testing"

	"github.com/aita/sqlkit/analysis"
	"github.com/aita/sqlkit/querytrace"
	"github.com/aita/sqlkit/sql"
)

// This file holds package-level, end-to-end querytrace tests that drive the
// whole pipeline (Config.Analyze -> Trace.RecordStatement -> Trace.Report)
// in-process. They are distinct from the database-backed integration tests under
// internal/tests; here a fakeParser stands in for a real SQL frontend.

// fakeParser is a stub sqlparse.Parser that returns a pre-built sql.Query,
// independent of the SQL text. The real parsers live in separate modules
// (sqlparse/postgres) the root module must not import, so these tests wire their
// own trivial parser to drive the full querytrace pipeline.
type fakeParser struct {
	query sql.Query
}

func (p fakeParser) Parse(text string) ([]sql.Query, error) {
	return []sql.Query{p.query}, nil
}

// TestLockingReadTriggersWriteWarningsE2E drives the whole querytrace pipeline
// for a locking read (SELECT ... FOR UPDATE): analysis classifies it write-like
// (LockingRead / IsWrite), so the per-statement write checks must fire under
// ReadOnly and ForbidWrites contexts and WriteCount must count it, while a plain
// SELECT does not.
func TestLockingReadTriggersWriteWarningsE2E(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	lockingQuery := sql.Select(sql.Col("id")).From(users).ForUpdate()
	plainQuery := sql.Select(sql.Col("id")).From(users)

	lockingCfg := querytrace.NewConfig(querytrace.WithParser(fakeParser{query: lockingQuery}))
	plainCfg := querytrace.NewConfig(querytrace.WithParser(fakeParser{query: plainQuery}))

	// 1. Analyze classifies the locking read write-like.
	qi := lockingCfg.Analyze("SELECT id FROM users FOR UPDATE", nil)
	if qi.Kind != analysis.StmtSelect {
		t.Fatalf("Kind = %v, want StmtSelect", qi.Kind)
	}
	if !qi.LockingRead {
		t.Fatalf("LockingRead = false, want true")
	}
	if !qi.IsWrite() {
		t.Fatalf("IsWrite() = false, want true")
	}

	// 2. End-to-end: a locking read under a ReadOnly context raises
	// write_in_read_only and counts as a write.
	t.Run("ReadOnly raises write_in_read_only", func(t *testing.T) {
		tr := querytrace.New("locking-read-only", querytrace.WithConfig(lockingCfg))
		ctx := querytrace.ReadOnly(context.Background())
		tr.RecordStatement(ctx, querytrace.Event{
			Kind:  querytrace.EventQuery,
			Query: lockingCfg.Analyze("SELECT id FROM users FOR UPDATE", nil),
		})

		report := tr.Report()
		if !hasWarning(report.Warnings, querytrace.WarnWriteInReadOnly) {
			t.Fatalf("warnings = %v, want one with code %s", report.Warnings, querytrace.WarnWriteInReadOnly)
		}
		if report.WriteCount != 1 {
			t.Fatalf("WriteCount = %d, want 1", report.WriteCount)
		}
	})

	// 3. Control: a plain SELECT under ReadOnly raises no write warning.
	t.Run("plain SELECT under ReadOnly is clean", func(t *testing.T) {
		tr := querytrace.New("plain-read-only", querytrace.WithConfig(plainCfg))
		ctx := querytrace.ReadOnly(context.Background())
		tr.RecordStatement(ctx, querytrace.Event{
			Kind:  querytrace.EventQuery,
			Query: plainCfg.Analyze("SELECT id FROM users", nil),
		})

		report := tr.Report()
		if hasWarning(report.Warnings, querytrace.WarnWriteInReadOnly) {
			t.Fatalf("plain SELECT raised %s: %v", querytrace.WarnWriteInReadOnly, report.Warnings)
		}
		if report.WriteCount != 0 {
			t.Fatalf("WriteCount = %d, want 0", report.WriteCount)
		}
	})

	// 4. A locking read under a ForbidWrites context raises write_forbidden.
	t.Run("ForbidWrites raises write_forbidden", func(t *testing.T) {
		tr := querytrace.New("locking-forbid-writes", querytrace.WithConfig(lockingCfg))
		ctx := querytrace.ForbidWrites(context.Background())
		tr.RecordStatement(ctx, querytrace.Event{
			Kind:  querytrace.EventQuery,
			Query: lockingCfg.Analyze("SELECT id FROM users FOR UPDATE", nil),
		})

		report := tr.Report()
		if !hasWarning(report.Warnings, querytrace.WarnWriteForbidden) {
			t.Fatalf("warnings = %v, want one with code %s", report.Warnings, querytrace.WarnWriteForbidden)
		}
	})
}

func hasWarning(warnings []querytrace.Warning, code querytrace.WarningCode) bool {
	for _, w := range warnings {
		if w.Code == code {
			return true
		}
	}
	return false
}
