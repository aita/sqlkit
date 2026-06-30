package integration

import (
	"context"
	stdsql "database/sql"
	"errors"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/integration/blogdb"
)

// These tests run the Before / AfterQuery / AfterExec hooks against a live
// engine. The unit tests cover the hook plumbing in isolation; here the hooks
// observe statements that actually execute, so the test confirms each fires with
// the right Op and compiled SQL, that context threads from Before into After, and
// that a Before error aborts the statement before it reaches the database.

func TestHooksFireAroundExecAndQuery(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()

		type ctxKey string
		const spanKey ctxKey = "span"

		var (
			beforeOps   []sqlkit.Op
			afterExecOK bool
			afterQryOK  bool
			ctxThreaded bool
		)
		hooks := sqlkit.Hooks{
			Before: func(ctx context.Context, stmt *sqlkit.Statement) (context.Context, error) {
				beforeOps = append(beforeOps, stmt.Op)
				if stmt.SQL == "" {
					t.Errorf("Before saw empty SQL for op %v", stmt.Op)
				}
				return context.WithValue(ctx, spanKey, "started"), nil
			},
			AfterExec: func(ctx context.Context, stmt sqlkit.Statement, res stdsql.Result, err error) error {
				if err == nil && stmt.Op == sqlkit.OpExec {
					afterExecOK = true
				}
				if ctx.Value(spanKey) == "started" {
					ctxThreaded = true
				}
				return nil
			},
			AfterQuery: func(ctx context.Context, stmt sqlkit.Statement, dest any, err error) error {
				if err == nil && stmt.Op == sqlkit.OpQuery {
					afterQryOK = true
				}
				return nil
			},
		}
		hdb := sqlkit.WrapDB(db.SQL(), sqlkit.WithDialect(db.Dialect()), sqlkit.WithHooks(hooks))

		// A write fires Before(OpExec) then AfterExec.
		if _, err := hdb.Insert(blogdb.Users).
			Values(userRow{Email: "hook@example.com"}).
			Exec(ctx); err != nil {
			t.Fatalf("hooked insert: %v", err)
		}
		// A read fires Before(OpQuery) then AfterQuery.
		var got []userRow
		if err := hdb.Select().From(blogdb.Users).
			Where(blogdb.Users.Email.Eq("hook@example.com")).
			All(ctx).Scan(&got); err != nil {
			t.Fatalf("hooked select: %v", err)
		}

		if !afterExecOK {
			t.Fatal("AfterExec did not fire for the insert")
		}
		if !afterQryOK {
			t.Fatal("AfterQuery did not fire for the select")
		}
		if !ctxThreaded {
			t.Fatal("context set in Before did not thread into AfterExec")
		}
		sawExec, sawQuery := false, false
		for _, op := range beforeOps {
			switch op {
			case sqlkit.OpExec:
				sawExec = true
			case sqlkit.OpQuery:
				sawQuery = true
			}
		}
		if !sawExec || !sawQuery {
			t.Fatalf("Before ops = %v, want both an exec and a query", beforeOps)
		}
	})
}

// TestHookBeforeErrorAborts confirms a Before hook returning an error stops the
// statement before it reaches the database: the row it would have inserted must
// not exist afterward.
func TestHookBeforeErrorAborts(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()

		abort := errors.New("aborted by hook")
		hdb := sqlkit.WrapDB(db.SQL(), sqlkit.WithDialect(db.Dialect()),
			sqlkit.WithHooks(sqlkit.Hooks{
				Before: func(ctx context.Context, stmt *sqlkit.Statement) (context.Context, error) {
					return ctx, abort
				},
			}))

		_, err := hdb.Insert(blogdb.Users).
			Values(userRow{Email: "aborted@example.com"}).
			Exec(ctx)
		if !errors.Is(err, abort) {
			t.Fatalf("insert error = %v, want the hook's abort error", err)
		}

		// The aborted insert never ran, so a direct read finds no such row.
		n, err := db.Select().From(blogdb.Users).
			Where(blogdb.Users.Email.Eq("aborted@example.com")).
			Count(ctx)
		if err != nil {
			t.Fatalf("count after abort: %v", err)
		}
		if n != 0 {
			t.Fatalf("rows after aborted insert = %d, want 0", n)
		}
	})
}
