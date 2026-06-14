package sqlkit_test

import (
	"reflect"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/sql"
)

func TestColumnEqAnyAndNeAll(t *testing.T) {
	// EqAny binds the whole list as one array parameter (PostgreSQL's IN
	// replacement), so a single $1 carries all values.
	q := sqlkit.Select(Users.ID).From(Users).Where(Users.ID.EqAny(1, 2, 3))
	gotSQL, args, err := q.ToSQL(sql.PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	wantSQL := `SELECT "public"."users"."id" FROM "public"."users" WHERE ("public"."users"."id" = ANY($1))`
	if gotSQL != wantSQL {
		t.Fatalf("ToSQL() = %q, want %q", gotSQL, wantSQL)
	}
	if want := []any{[]int64{1, 2, 3}}; !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}

	assertToSQL(t,
		sqlkit.Select(Users.ID).From(Users).Where(Users.ID.NeAll(4, 5)),
		`SELECT "public"."users"."id" FROM "public"."users" WHERE ("public"."users"."id" <> ALL($1))`,
		[]any{[]int64{4, 5}})
}
