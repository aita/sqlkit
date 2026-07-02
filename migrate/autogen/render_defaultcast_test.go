package autogen

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit/decl"
	"github.com/aita/sqlkit/sql"
)

// TestRenderPostgresDefaultCastNotSpuriousDiff covers --from-db: PostgreSQL's
// pg_get_expr renders a literal default with an explicit trailing type cast
// (e.g. "'active'::character varying"), which introspection carries verbatim,
// while a hand-written decl.Default literal normally omits the cast (e.g.
// "'active'"). Comparing the two by exact text previously reported a spurious
// ModifyColumn on every run even though the default never changed.
func TestRenderPostgresDefaultCastNotSpuriousDiff(t *testing.T) {
	cases := []struct {
		name         string
		introspected string
		declared     string
	}{
		{"varchar literal", "'active'::character varying", "'active'"},
		{"numeric literal", "0::numeric", "0"},
		{"numeric with precision", "0::numeric(10,2)", "0"},
		{"jsonb literal", "'{}'::jsonb", "'{}'"},
		{"array literal", "ARRAY[]::text[]", "ARRAY[]"},
		{"enum literal", "'foo'::my_status", "'foo'"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			source := decl.NewCatalog(decl.DefaultSchema("public"))
			source.Table("users",
				decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
				decl.Column("status", decl.Text, decl.Default(sql.Raw(c.introspected))),
			)
			target := decl.NewCatalog(decl.DefaultSchema("public"))
			target.Table("users",
				decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
				decl.Column("status", decl.Text, decl.Default(sql.Raw(c.declared))),
			)

			res := render(t, build(t, source), build(t, target), "postgres", Renames{})
			if strings.Contains(res.Up, `op.ModifyColumn`) {
				t.Errorf("Up unexpectedly reports a default change for %q vs %q:\n%s", c.introspected, c.declared, res.Up)
			}
		})
	}
}

// TestRenderPostgresDefaultCastStillDetectsRealChange ensures the comparison
// normalization does not mask a genuine default value change: only the
// trailing "::type" cast is ignored, not the literal itself.
func TestRenderPostgresDefaultCastStillDetectsRealChange(t *testing.T) {
	source := decl.NewCatalog(decl.DefaultSchema("public"))
	source.Table("users",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("status", decl.Text, decl.Default(sql.Raw("'active'::character varying"))),
	)
	target := decl.NewCatalog(decl.DefaultSchema("public"))
	target.Table("users",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("status", decl.Text, decl.Default(sql.Raw("'inactive'"))),
	)

	res := render(t, build(t, source), build(t, target), "postgres", Renames{})
	if !strings.Contains(res.Up, `op.ModifyColumn`) {
		t.Errorf("Up should report a default change ('active' -> 'inactive'):\n%s", res.Up)
	}
}

// TestRenderPostgresCheckCastNotSpuriousDiff covers the same normalization for
// CHECK constraints: pg_get_constraintdef can render a cast inside the clause
// text on the value side, which a hand-written decl.Check normally omits.
func TestRenderPostgresCheckCastNotSpuriousDiff(t *testing.T) {
	source := decl.NewCatalog(decl.DefaultSchema("public"))
	source.Table("users",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Check(sql.Raw("price > 0::numeric")),
	)
	target := decl.NewCatalog(decl.DefaultSchema("public"))
	target.Table("users",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Check(sql.Raw("price > 0")),
	)

	res := render(t, build(t, source), build(t, target), "postgres", Renames{})
	if strings.Contains(res.Up, `op.DropConstraint`) || strings.Contains(res.Up, `op.AddCheck`) {
		t.Errorf("Up unexpectedly reports a check change for a cast-only difference:\n%s", res.Up)
	}
}

// TestRenderMySQLDefaultCastStillCompared ensures the PostgreSQL-only
// normalization is not applied on MySQL, which does not render this kind of
// trailing cast and whose default text should still be compared as-is.
func TestRenderMySQLDefaultCastStillCompared(t *testing.T) {
	source := decl.NewCatalog(decl.DefaultSchema("public"))
	source.Table("users",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("status", decl.VarChar(20), decl.Default(sql.Raw("'active'"))),
	)
	target := decl.NewCatalog(decl.DefaultSchema("public"))
	target.Table("users",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("status", decl.VarChar(20), decl.Default(sql.Raw("'active'::not_a_real_cast"))),
	)

	res := render(t, build(t, source), build(t, target), "mysql", Renames{})
	if !strings.Contains(res.Up, `op.ModifyColumn`) {
		t.Errorf("Up should report a default change on mysql (no cast-stripping there):\n%s", res.Up)
	}
}
