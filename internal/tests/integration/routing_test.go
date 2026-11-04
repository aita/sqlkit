package integration

import (
	"context"
	stdsql "database/sql"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/integration/blogdb"
)

// countingBackend wraps a real Backend and tallies the statements routed to it,
// so a routing test can assert which target each statement reached while still
// executing it against a live engine.
type countingBackend struct {
	sqlkit.Backend
	queries int
	execs   int
}

func (b *countingBackend) Query(ctx context.Context, stmt sqlkit.Statement) (*stdsql.Rows, error) {
	b.queries++
	return b.Backend.Query(ctx, stmt)
}

func (b *countingBackend) Exec(ctx context.Context, stmt sqlkit.Statement) (stdsql.Result, error) {
	b.execs++
	return b.Backend.Exec(ctx, stmt)
}

// TestReadWriteRouting drives the read/write-splitting router against a real
// engine. Both backends point at the same server (so the data stays consistent
// and assertions are simple), but they are distinct Backend objects, so the
// per-backend counters reveal the routing decision: reads must land on the
// replica and writes on the primary, while the statements still execute for real.
func TestReadWriteRouting(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()

		primary := &countingBackend{Backend: sqlkit.DB(db.SQL())}
		replica := &countingBackend{Backend: sqlkit.DB(db.SQL())}
		router := sqlkit.NewRouter(map[string]sqlkit.Backend{
			"default": primary,
			"replica": replica,
		}, sqlkit.ReadWriteRouter(sqlkit.WithReplicas("replica")))
		rdb := sqlkit.WrapExecutor(router, sqlkit.WithDialect(db.Dialect()))

		// A write must route to the primary, not the replica.
		if _, err := rdb.Insert(blogdb.Users).
			Values(userRow{Email: "router@example.com"}).
			Exec(ctx); err != nil {
			t.Fatalf("routed insert: %v", err)
		}
		if primary.execs != 1 {
			t.Fatalf("primary execs = %d, want 1 (write routed to primary)", primary.execs)
		}
		if replica.execs != 0 {
			t.Fatalf("replica execs = %d, want 0 (no write on replica)", replica.execs)
		}

		// A read must route to the replica and still see the just-written row
		// (same underlying server), proving the statement actually executed.
		n, err := rdb.Select().From(blogdb.Users).
			Where(blogdb.Users.Email.Eq("router@example.com")).
			Count(ctx)
		if err != nil {
			t.Fatalf("routed count: %v", err)
		}
		if n != 1 {
			t.Fatalf("routed read count = %d, want 1", n)
		}
		if replica.queries == 0 {
			t.Fatalf("replica queries = 0, want the read routed to the replica")
		}
	})
}
