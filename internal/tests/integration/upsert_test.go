package integration

import (
	"context"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/integration/blogdb"
)

// TestUpsert covers INSERT ... ON CONFLICT DO UPDATE end to end on both engines.
// OnConflict renders PostgreSQL's ON CONFLICT and MySQL's ON DUPLICATE KEY
// UPDATE; the call is written once and stays portable because MySQL keys on any
// unique/primary key and ignores the conflict-target column list. The users.email
// column is UNIQUE, so a second write with the same email collides.
func TestUpsert(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()
		aliceID := insertUser(t, db, eng, userRow{Email: "alice@example.com", Name: ptr("Alice")})

		// Conflict path: the same unique email updates the existing row in place
		// (Name.Excluded() refers to the proposed row) rather than inserting a
		// duplicate, so the row count stays at one and alice's name changes.
		if _, err := db.Insert(blogdb.Users).
			Values(userRow{Email: "alice@example.com", Name: ptr("Alice Updated")}).
			OnConflict(blogdb.Users.Email).
			DoUpdate(blogdb.Users.Name.SetExpr(blogdb.Users.Name.Excluded())).
			Exec(ctx); err != nil {
			t.Fatalf("upsert (conflict path): %v", err)
		}
		total, err := db.Select().From(blogdb.Users).Count(ctx)
		if err != nil {
			t.Fatalf("count after conflicting upsert: %v", err)
		}
		if total != 1 {
			t.Fatalf("user count after conflicting upsert = %d, want 1 (updated, not inserted)", total)
		}
		if name := userName(t, db, aliceID); name != "Alice Updated" {
			t.Fatalf("name after upsert = %q, want Alice Updated", name)
		}

		// Insert path: a fresh email has no conflict, so the same upsert statement
		// inserts a new row.
		if _, err := db.Insert(blogdb.Users).
			Values(userRow{Email: "newcomer@example.com", Name: ptr("Newcomer")}).
			OnConflict(blogdb.Users.Email).
			DoUpdate(blogdb.Users.Name.SetExpr(blogdb.Users.Name.Excluded())).
			Exec(ctx); err != nil {
			t.Fatalf("upsert (insert path): %v", err)
		}
		if total, err := db.Select().From(blogdb.Users).Count(ctx); err != nil || total != 2 {
			t.Fatalf("count after non-conflicting upsert = %d, %v; want 2", total, err)
		}
	})
}
