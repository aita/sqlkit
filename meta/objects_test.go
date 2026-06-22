package meta_test

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit/meta"
	"github.com/aita/sqlkit/sql"
)

func i64(v int64) *int64 { return &v }
func boolp(v bool) *bool { return &v }

// ---- Extension --------------------------------------------------------------

func TestExtensionStatements(t *testing.T) {
	pg := mustDialect(t, "postgres")

	got, _, err := meta.Extension{Name: "uuid-ossp"}.CreateStatement().ToSQL(pg)
	if err != nil {
		t.Fatalf("CreateStatement().ToSQL() error = %v", err)
	}
	if want := `CREATE EXTENSION IF NOT EXISTS "uuid-ossp";`; got != want {
		t.Errorf("create = %q, want %q", got, want)
	}

	got, _, err = meta.Extension{Name: "hstore", Schema: "ext"}.CreateStatement().ToSQL(pg)
	if err != nil {
		t.Fatalf("CreateStatement().ToSQL() error = %v", err)
	}
	if want := `CREATE EXTENSION IF NOT EXISTS "hstore" WITH SCHEMA "ext";`; got != want {
		t.Errorf("create with schema = %q, want %q", got, want)
	}

	got, _, err = meta.Extension{Name: "uuid-ossp"}.DropStatement().ToSQL(pg)
	if err != nil {
		t.Fatalf("DropStatement().ToSQL() error = %v", err)
	}
	if want := `DROP EXTENSION IF EXISTS "uuid-ossp";`; got != want {
		t.Errorf("drop = %q, want %q", got, want)
	}
}

// ---- Enum -------------------------------------------------------------------

func TestEnumStatements(t *testing.T) {
	pg := mustDialect(t, "postgres")
	e := meta.Enum{Schema: "app", Name: "mood", Values: []string{"happy", "sad"}}

	if got, want := e.QualifiedName(), "app.mood"; got != want {
		t.Errorf("QualifiedName() = %q, want %q", got, want)
	}
	if got, want := (meta.Enum{Name: "mood"}).QualifiedName(), "mood"; got != want {
		t.Errorf("unqualified QualifiedName() = %q, want %q", got, want)
	}

	got, _, err := e.CreateStatement().ToSQL(pg)
	if err != nil {
		t.Fatalf("CreateStatement().ToSQL() error = %v", err)
	}
	if want := `CREATE TYPE "app"."mood" AS ENUM ('happy', 'sad');`; got != want {
		t.Errorf("create = %q, want %q", got, want)
	}

	got, _, err = e.DropStatement().ToSQL(pg)
	if err != nil {
		t.Fatalf("DropStatement().ToSQL() error = %v", err)
	}
	if want := `DROP TYPE IF EXISTS "app"."mood";`; got != want {
		t.Errorf("drop = %q, want %q", got, want)
	}
}

func TestEnumEqual(t *testing.T) {
	base := meta.Enum{Schema: "app", Name: "mood", Values: []string{"happy", "sad"}}
	if !base.Equal(meta.Enum{Schema: "app", Name: "mood", Values: []string{"happy", "sad"}}) {
		t.Error("identical enums should be Equal")
	}
	cases := map[string]meta.Enum{
		"schema":      {Schema: "other", Name: "mood", Values: []string{"happy", "sad"}},
		"name":        {Schema: "app", Name: "feeling", Values: []string{"happy", "sad"}},
		"value count": {Schema: "app", Name: "mood", Values: []string{"happy"}},
		"value order": {Schema: "app", Name: "mood", Values: []string{"sad", "happy"}},
		"value label": {Schema: "app", Name: "mood", Values: []string{"happy", "blue"}},
	}
	for name, other := range cases {
		t.Run(name, func(t *testing.T) {
			if base.Equal(other) {
				t.Errorf("enums differing in %s should not be Equal", name)
			}
		})
	}
}

// ---- Sequence ---------------------------------------------------------------

func TestSequenceStatements(t *testing.T) {
	pg := mustDialect(t, "postgres")

	full := meta.Sequence{
		Schema:    "app",
		Name:      "s",
		Increment: i64(5),
		Start:     i64(10),
		MinValue:  i64(1),
		MaxValue:  i64(100),
		Cache:     i64(2),
		Cycle:     boolp(true),
		OwnedBy:   "app.t.id",
	}
	if got, want := full.QualifiedName(), "app.s"; got != want {
		t.Errorf("QualifiedName() = %q, want %q", got, want)
	}
	got, _, err := full.CreateStatement().ToSQL(pg)
	if err != nil {
		t.Fatalf("CreateStatement().ToSQL() error = %v", err)
	}
	for _, want := range []string{
		"INCREMENT BY 5", "MINVALUE 1", "MAXVALUE 100",
		"START WITH 10", "CACHE 2", "CYCLE",
		`OWNED BY "app"."t"."id"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("create missing %q:\n%s", want, got)
		}
	}

	// NoCycle branch (Cycle=false) and the minimal option set.
	got, _, err = (meta.Sequence{Name: "s", Cycle: boolp(false)}).CreateStatement().ToSQL(pg)
	if err != nil {
		t.Fatalf("CreateStatement().ToSQL() error = %v", err)
	}
	if !strings.Contains(got, "NO CYCLE") {
		t.Errorf("Cycle=false should render NO CYCLE:\n%s", got)
	}

	// A sequence with no options set renders none of them.
	got, _, err = (meta.Sequence{Name: "s"}).CreateStatement().ToSQL(pg)
	if err != nil {
		t.Fatalf("CreateStatement().ToSQL() error = %v", err)
	}
	for _, unwanted := range []string{"INCREMENT", "MINVALUE", "MAXVALUE", "START", "CACHE", "CYCLE", "OWNED BY"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("unset option %q should not render:\n%s", unwanted, got)
		}
	}

	got, _, err = full.DropStatement().ToSQL(pg)
	if err != nil {
		t.Fatalf("DropStatement().ToSQL() error = %v", err)
	}
	if want := `DROP SEQUENCE IF EXISTS "app"."s";`; got != want {
		t.Errorf("drop = %q, want %q", got, want)
	}
}

func TestSequenceEqual(t *testing.T) {
	base := meta.Sequence{
		Schema: "app", Name: "s",
		Increment: i64(5), Start: i64(10), MinValue: i64(1), MaxValue: i64(100),
		Cache: i64(2), Cycle: boolp(true), OwnedBy: "app.t.id",
	}
	clone := base
	if !base.Equal(clone) {
		t.Error("identical sequences should be Equal")
	}

	cases := map[string]func(*meta.Sequence){
		"schema":    func(s *meta.Sequence) { s.Schema = "other" },
		"name":      func(s *meta.Sequence) { s.Name = "other" },
		"increment": func(s *meta.Sequence) { s.Increment = i64(7) },
		"start":     func(s *meta.Sequence) { s.Start = i64(11) },
		"minvalue":  func(s *meta.Sequence) { s.MinValue = i64(0) },
		"maxvalue":  func(s *meta.Sequence) { s.MaxValue = i64(99) },
		"cache":     func(s *meta.Sequence) { s.Cache = i64(3) },
		"cycle":     func(s *meta.Sequence) { s.Cycle = boolp(false) },
		"ownedby":   func(s *meta.Sequence) { s.OwnedBy = "app.t.other" },
		"nil ptr":   func(s *meta.Sequence) { s.Increment = nil },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			other := base
			mutate(&other)
			if base.Equal(other) {
				t.Errorf("sequences differing in %s should not be Equal", name)
			}
		})
	}
}

// TestEqInt64Ptr exercises the pointer-comparison helper through Sequence.Equal,
// covering the both-nil, one-nil, and both-set value paths.
func TestEqInt64Ptr(t *testing.T) {
	bothNil := meta.Sequence{Name: "s"}
	if !bothNil.Equal(meta.Sequence{Name: "s"}) {
		t.Error("two sequences with all-nil options should be Equal")
	}
	oneNil := meta.Sequence{Name: "s", Increment: i64(1)}
	if oneNil.Equal(meta.Sequence{Name: "s"}) {
		t.Error("set vs nil pointer should not be Equal")
	}
	if (meta.Sequence{Name: "s"}).Equal(oneNil) {
		t.Error("nil vs set pointer should not be Equal")
	}
	// Both set, equal values.
	if !(meta.Sequence{Name: "s", Increment: i64(2)}).Equal(meta.Sequence{Name: "s", Increment: i64(2)}) {
		t.Error("equal set pointers should be Equal")
	}
}

func TestEqBoolPtr(t *testing.T) {
	if (meta.Sequence{Name: "s", Cycle: boolp(true)}).Equal(meta.Sequence{Name: "s"}) {
		t.Error("set vs nil Cycle should not be Equal")
	}
	if (meta.Sequence{Name: "s"}).Equal(meta.Sequence{Name: "s", Cycle: boolp(true)}) {
		t.Error("nil vs set Cycle should not be Equal")
	}
	if !(meta.Sequence{Name: "s", Cycle: boolp(false)}).Equal(meta.Sequence{Name: "s", Cycle: boolp(false)}) {
		t.Error("equal Cycle pointers should be Equal")
	}
}

// ---- MaterializedView -------------------------------------------------------

func mvQuery() sql.Query {
	return sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("t")})
}

func TestMaterializedViewStatements(t *testing.T) {
	pg := mustDialect(t, "postgres")
	mv := meta.MaterializedView{Schema: "app", Name: "v", Query: mvQuery(), Columns: []string{"id"}, WithNoData: true}

	if got, want := mv.QualifiedName(), "app.v"; got != want {
		t.Errorf("QualifiedName() = %q, want %q", got, want)
	}

	got, _, err := mv.CreateStatement().ToSQL(pg)
	if err != nil {
		t.Fatalf("CreateStatement().ToSQL() error = %v", err)
	}
	for _, want := range []string{
		`CREATE MATERIALIZED VIEW "app"."v"`,
		`("id")`,
		"WITH NO DATA",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("create missing %q:\n%s", want, got)
		}
	}

	// Without columns or WITH NO DATA, neither renders.
	plain := meta.MaterializedView{Name: "v", Query: mvQuery()}
	got, _, err = plain.CreateStatement().ToSQL(pg)
	if err != nil {
		t.Fatalf("CreateStatement().ToSQL() error = %v", err)
	}
	if strings.Contains(got, "WITH NO DATA") {
		t.Errorf("WithNoData unset should not render WITH NO DATA:\n%s", got)
	}

	got, _, err = mv.DropStatement().ToSQL(pg)
	if err != nil {
		t.Fatalf("DropStatement().ToSQL() error = %v", err)
	}
	if want := `DROP MATERIALIZED VIEW IF EXISTS "app"."v";`; got != want {
		t.Errorf("drop = %q, want %q", got, want)
	}
}

// ---- Grant ------------------------------------------------------------------

func TestGrantStatements(t *testing.T) {
	pg := mustDialect(t, "postgres")

	// Default object type (TABLE) and explicit privileges.
	g := meta.Grant{Privileges: []string{"SELECT", "INSERT"}, Objects: []string{"t"}, Grantees: []string{"r"}}
	got, _, err := g.CreateStatement().ToSQL(pg)
	if err != nil {
		t.Fatalf("CreateStatement().ToSQL() error = %v", err)
	}
	if want := `GRANT SELECT, INSERT ON TABLE "t" TO "r";`; got != want {
		t.Errorf("grant = %q, want %q", got, want)
	}
	got, _, err = g.DropStatement().ToSQL(pg)
	if err != nil {
		t.Fatalf("DropStatement().ToSQL() error = %v", err)
	}
	if want := `REVOKE SELECT, INSERT ON TABLE "t" FROM "r";`; got != want {
		t.Errorf("revoke = %q, want %q", got, want)
	}

	// Empty privileges => ALL PRIVILEGES; explicit object type; WITH GRANT OPTION.
	all := meta.Grant{ObjectType: "SEQUENCE", Objects: []string{"s"}, Grantees: []string{"r"}, WithGrantOption: true}
	got, _, err = all.CreateStatement().ToSQL(pg)
	if err != nil {
		t.Fatalf("CreateStatement().ToSQL() error = %v", err)
	}
	for _, want := range []string{"ALL PRIVILEGES", "ON SEQUENCE", "WITH GRANT OPTION"} {
		if !strings.Contains(got, want) {
			t.Errorf("grant missing %q:\n%s", want, got)
		}
	}
	got, _, err = all.DropStatement().ToSQL(pg)
	if err != nil {
		t.Fatalf("DropStatement().ToSQL() error = %v", err)
	}
	if !strings.Contains(got, "ON SEQUENCE") {
		t.Errorf("revoke missing ON SEQUENCE:\n%s", got)
	}
}

func TestGrantEqual(t *testing.T) {
	base := meta.Grant{
		Privileges: []string{"SELECT"}, ObjectType: "TABLE",
		Objects: []string{"t"}, Grantees: []string{"r"}, WithGrantOption: true,
	}
	clone := base
	if !base.Equal(clone) {
		t.Error("identical grants should be Equal")
	}
	cases := map[string]meta.Grant{
		"object type":       {Privileges: []string{"SELECT"}, ObjectType: "SEQUENCE", Objects: []string{"t"}, Grantees: []string{"r"}, WithGrantOption: true},
		"with grant option": {Privileges: []string{"SELECT"}, ObjectType: "TABLE", Objects: []string{"t"}, Grantees: []string{"r"}},
		"privileges":        {Privileges: []string{"INSERT"}, ObjectType: "TABLE", Objects: []string{"t"}, Grantees: []string{"r"}, WithGrantOption: true},
		"privilege count":   {Privileges: []string{"SELECT", "INSERT"}, ObjectType: "TABLE", Objects: []string{"t"}, Grantees: []string{"r"}, WithGrantOption: true},
		"objects":           {Privileges: []string{"SELECT"}, ObjectType: "TABLE", Objects: []string{"u"}, Grantees: []string{"r"}, WithGrantOption: true},
		"grantees":          {Privileges: []string{"SELECT"}, ObjectType: "TABLE", Objects: []string{"t"}, Grantees: []string{"q"}, WithGrantOption: true},
	}
	for name, other := range cases {
		t.Run(name, func(t *testing.T) {
			if base.Equal(other) {
				t.Errorf("grants differing in %s should not be Equal", name)
			}
		})
	}
}
