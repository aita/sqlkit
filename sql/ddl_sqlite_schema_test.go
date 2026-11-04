package sql

import (
	"strings"
	"testing"
)

// SQLite puts a schema qualifier on the index name, not the table, and its
// REFERENCES takes a bare table in the referencing table's own schema:
// CREATE INDEX "i" ON "main"."t" and REFERENCES "main"."p" are syntax errors.
func TestSQLiteSchemaQualifiedIndexAndReferences(t *testing.T) {
	tbl := Table{Schema: NewIdent("main"), Name: NewIdent("t")}
	fk := ForeignKeyConstraint{Columns: []Ident{NewIdent("a")}, RefSchema: NewIdent("main"), RefTable: NewIdent("p"), RefColumns: []Ident{NewIdent("id")}}
	colRef := ColumnDef{Name: NewIdent("b"), Type: EngineType{Name: "INTEGER"},
		References: &ColumnReference{Schema: NewIdent("main"), Table: NewIdent("p"), Column: NewIdent("id")}}
	cases := []struct {
		q    Query
		want string
	}{
		{CreateIndex("i", tbl, "a"), `CREATE INDEX "main"."i" ON "t" ("a");`},
		{CreateIndex("i", Table{Name: NewIdent("t")}, "a"), `CREATE INDEX "i" ON "t" ("a");`},
		{CreateTable(tbl).Column(ColumnDef{Name: NewIdent("a"), Type: EngineType{Name: "INTEGER"}}).ForeignKey(fk), `REFERENCES "p" ("id")`},
		{CreateTable(tbl).Column(colRef), `REFERENCES "p" ("id")`},
	}
	for _, d := range []Dialect{SQLiteDialect{}, LibSQLDialect{}} {
		for _, tc := range cases {
			got, _, err := tc.q.ToSQL(d)
			if err != nil || !strings.Contains(got, tc.want) {
				t.Errorf("%s: got %s (err %v), want it to contain %s", d.Name(), got, err, tc.want)
			}
		}
		// A reference into another schema can't be expressed.
		other := fk
		other.RefSchema = NewIdent("aux")
		if _, _, err := CreateTable(tbl).Column(ColumnDef{Name: NewIdent("a"), Type: EngineType{Name: "INTEGER"}}).ForeignKey(other).ToSQL(d); err == nil ||
			!strings.Contains(err.Error(), "another schema") {
			t.Errorf("%s: cross-schema reference err = %v", d.Name(), err)
		}
	}
	// PostgreSQL keeps both qualifiers.
	if got, _, _ := CreateIndex("i", tbl, "a").ToSQL(PostgreSQLDialect{}); got != `CREATE INDEX "i" ON "main"."t" ("a");` {
		t.Errorf("postgres: %s", got)
	}
}
