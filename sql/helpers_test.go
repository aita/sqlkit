package sql

import (
	"reflect"
	"testing"
)

func mustDialect(t *testing.T, name string) Dialect {
	t.Helper()
	d, err := DialectByName(name)
	if err != nil {
		t.Fatalf("DialectByName(%q) error = %v", name, err)
	}
	return d
}

func assertMySQL(t *testing.T, query Query, wantSQL string, wantArgs []any) {
	t.Helper()
	sqlText, args, err := query.ToSQL(MySQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	if sqlText != wantSQL {
		t.Fatalf("ToSQL() SQL = %q,\nwant %q", sqlText, wantSQL)
	}
	if len(wantArgs) > 0 && len(args) != len(wantArgs) {
		t.Fatalf("ToSQL() args = %#v, want %#v", args, wantArgs)
	}
}

var (
	users = Table{Schema: NewIdent("public"), Name: NewIdent("users")}
	orgs  = Table{Name: NewIdent("orgs")}
)

func assertSQL(t *testing.T, query Query, wantSQL string, wantArgs []any) {
	t.Helper()
	sqlText, args, err := query.ToSQL(PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	if sqlText != wantSQL {
		t.Fatalf("ToSQL() SQL = %q, want %q", sqlText, wantSQL)
	}
	if len(wantArgs) == 0 {
		if len(args) != 0 {
			t.Fatalf("ToSQL() args = %#v, want none", args)
		}
		return
	}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("ToSQL() args = %#v, want %#v", args, wantArgs)
	}
}

func assertStandard(t *testing.T, query Query, wantSQL string, wantArgs []any) {
	t.Helper()
	sqlText, args, err := query.ToSQL(StandardSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	if sqlText != wantSQL {
		t.Fatalf("ToSQL() SQL = %q,\nwant %q", sqlText, wantSQL)
	}
	if len(wantArgs) > 0 && len(args) != len(wantArgs) {
		t.Fatalf("ToSQL() args = %#v, want %#v", args, wantArgs)
	}
}
