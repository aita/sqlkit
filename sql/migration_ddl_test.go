package sql

import "testing"

func TestMigrationDDLCompile(t *testing.T) {
	cases := []struct {
		stmt Statement
		want string
	}{
		{CreateExtension("pgcrypto").IfNotExists(), `CREATE EXTENSION IF NOT EXISTS "pgcrypto";`},
		{CreateExtension("uuid-ossp").Schema("ext"), `CREATE EXTENSION "uuid-ossp" WITH SCHEMA "ext";`},
		{DropExtension("a", "b").IfExists(), `DROP EXTENSION IF EXISTS "a", "b";`},
		{CreateEnum("mood", "sad", "happy"), `CREATE TYPE "mood" AS ENUM ('sad', 'happy');`},
		{DropType("mood").IfExists().Cascade(), `DROP TYPE IF EXISTS "mood" CASCADE;`},
		{RenameTable(Table{Schema: "app", Name: "users"}, "people"), `ALTER TABLE "app"."users" RENAME TO "people";`},
		{RenameColumn(Table{Name: "users"}, "email", "addr"), `ALTER TABLE "users" RENAME COLUMN "email" TO "addr";`},
	}
	for _, tc := range cases {
		got, args, err := tc.stmt.ToSQL(PostgreSQLDialect{})
		if err != nil {
			t.Fatalf("ToSQL() error = %v", err)
		}
		if got != tc.want {
			t.Errorf("ToSQL() = %q, want %q", got, tc.want)
		}
		if len(args) != 0 {
			t.Errorf("args = %#v, want none", args)
		}
	}
}

func TestMigrationDDLValidation(t *testing.T) {
	if _, _, err := CreateExtension("").ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("CREATE EXTENSION without a name succeeded, want error")
	}
	if _, _, err := RenameColumn(Table{Name: "t"}, "a", "").ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("RENAME COLUMN without a new name succeeded, want error")
	}
}
