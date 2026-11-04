package sql

import (
	"strings"
	"testing"
)

// An unnamed CHECK or FOREIGN KEY constraint must omit the CONSTRAINT keyword
// (the engine names it) rather than render CONSTRAINT "", which PostgreSQL
// rejects as a zero-length identifier — as an unnamed UNIQUE already does.
func TestUnnamedConstraintsOmitConstraintKeyword(t *testing.T) {
	tbl := Table{Name: NewIdent("t")}
	fk := ForeignKeyConstraint{Columns: []Ident{NewIdent("a")}, RefTable: NewIdent("p"), RefColumns: []Ident{NewIdent("id")}}
	queries := []Query{
		CreateTable(tbl).Column(ColumnDef{Name: NewIdent("a"), Type: EngineType{Name: "int"}}).
			Check("", Raw("a > 0")).ForeignKey(fk),
		AlterTable(tbl).AddCheck("", Raw("a > 0")),
		AlterTable(tbl).AddForeignKey(fk),
	}
	for _, d := range []Dialect{PostgreSQLDialect{}, MySQLDialect{}} {
		for _, q := range queries {
			got, _, err := q.ToSQL(d)
			if err != nil {
				t.Fatalf("%s: %v", d.Name(), err)
			}
			if strings.Contains(got, "CONSTRAINT") {
				t.Errorf("%s: %s, want no CONSTRAINT keyword for an unnamed constraint", d.Name(), got)
			}
			if !strings.Contains(got, "CHECK (a > 0)") && !strings.Contains(got, "FOREIGN KEY (") {
				t.Errorf("%s: %s", d.Name(), got)
			}
		}
	}
	// A named constraint keeps its name.
	got, _, _ := AlterTable(tbl).AddCheck("pos", Raw("a > 0")).ToSQL(PostgreSQLDialect{})
	if !strings.Contains(got, `ADD CONSTRAINT "pos" CHECK (a > 0)`) {
		t.Errorf("named: %s", got)
	}
}
