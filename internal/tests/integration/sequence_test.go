package integration

import (
	"context"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/sql"
)

// TestSequence covers PostgreSQL's standalone sequences. The builder's
// CreateSequence (with START / INCREMENT) and DropSequence DDL must execute, and
// nextval has to advance by the configured step. MySQL has no CREATE SEQUENCE
// (it uses AUTO_INCREMENT), so this skips there.
func TestSequence(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		if eng.name != "postgres" {
			t.Skipf("%s has no CREATE SEQUENCE", eng.name)
		}
		ctx := context.Background()

		_, _ = db.Exec(ctx, sql.DropSequence("test_seq").IfExists())
		if _, err := db.Exec(ctx, sql.CreateSequence(sql.Tbl("test_seq")).Start(100).Increment(5)); err != nil {
			t.Fatalf("create sequence: %v", err)
		}
		t.Cleanup(func() {
			_, _ = db.Exec(context.Background(), sql.DropSequence("test_seq").IfExists())
		})

		// Two successive nextval calls must yield the start value and start+step.
		var first, second int64
		if err := db.SQL().QueryRowContext(ctx, "SELECT nextval('test_seq')").Scan(&first); err != nil {
			t.Fatalf("first nextval: %v", err)
		}
		if err := db.SQL().QueryRowContext(ctx, "SELECT nextval('test_seq')").Scan(&second); err != nil {
			t.Fatalf("second nextval: %v", err)
		}
		if first != 100 || second != 105 {
			t.Fatalf("nextval sequence = %d, %d; want 100, 105 (START 100 INCREMENT 5)", first, second)
		}
	})
}
