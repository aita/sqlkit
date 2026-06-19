package decl_test

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit/codegen"
	"github.com/aita/sqlkit/decl"
	querysql "github.com/aita/sqlkit/sql"
)

func TestEnumColumnType(t *testing.T) {
	catalog := decl.NewCatalog(decl.DefaultSchema("public"))
	catalog.Enum("mood", "happy", "sad")
	catalog.Table("users",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("mood", decl.EnumType("mood")),
		decl.Column("backup_mood", decl.EnumType("mood"), decl.Nullable()),
	)
	md := buildCatalog(t, catalog)

	// DDL renders the column with the enum type name.
	ddl, err := md.CreateSQL(querysql.PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("CreateSQL: %v", err)
	}
	if !strings.Contains(ddl, `"mood" mood`) {
		t.Fatalf("expected enum column type in DDL:\n%s", ddl)
	}

	// codegen maps the column to the generated Go enum type (nullable -> pointer).
	files, err := codegen.Generate(md, codegen.Options{PackageName: "appdb"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var tables, enums string
	for _, f := range files {
		switch f.Name {
		case "tables.go":
			tables = string(f.Content)
		case "enums.go":
			enums = string(f.Content)
		}
	}
	if !strings.Contains(enums, "type Mood string") {
		t.Fatalf("enums.go missing Mood type:\n%s", enums)
	}
	for _, want := range []string{"Column[Mood]", "Column[*Mood]"} {
		if !strings.Contains(tables, want) {
			t.Fatalf("tables.go missing %q in:\n%s", want, tables)
		}
	}
}

func TestEnumColumnUnavailableOnMySQL(t *testing.T) {
	catalog := decl.NewCatalog()
	catalog.Enum("mood", "happy")
	catalog.Table("users",
		decl.Column("id", decl.BigInt, decl.PrimaryKey()),
		decl.Column("mood", decl.EnumType("mood")),
	)
	md := buildCatalog(t, catalog)
	if _, err := md.CreateSQL(querysql.MySQLDialect{}); err == nil {
		t.Fatal("expected MySQL CreateSQL to fail for an enum column (PostgreSQL-only)")
	}
}
