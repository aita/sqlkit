package postgres_test

import (
	"testing"

	"github.com/aita/sqlkit/sql"
)

func TestArraySubscript(t *testing.T) {
	assertRoundTrip(t,
		"SELECT tags[1] FROM t",
		sql.Select(sql.Subscript(sql.Col("tags"), 1)).From(sql.Table{Name: "t"}),
		`SELECT "tags"[$1] FROM "t"`,
		1)
}

func TestArraySlice(t *testing.T) {
	assertRoundTrip(t,
		"SELECT tags[1:3] FROM t",
		sql.Select(sql.Slice(sql.Col("tags"), sql.Val(1), sql.Val(3))).From(sql.Table{Name: "t"}),
		`SELECT "tags"[$1:$2] FROM "t"`,
		1, 3)
}

func TestArraySubscriptInWhere(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE tags[1] = 'x'",
		selWhere(sql.Eq(sql.Subscript(sql.Col("tags"), 1), "x")),
		`SELECT "id" FROM "t" WHERE ("tags"[$1] = $2)`,
		1, "x")
}

func TestCompositeField(t *testing.T) {
	assertRoundTrip(t,
		"SELECT (rec).name FROM t",
		sql.Select(sql.Field(sql.Col("rec"), "name")).From(sql.Table{Name: "t"}),
		`SELECT ("rec")."name" FROM "t"`)
}

func TestCollate(t *testing.T) {
	assertRoundTrip(t,
		`SELECT id FROM t ORDER BY name COLLATE "C"`,
		sql.Select(sql.Col("id")).From(sql.Table{Name: "t"}).
			OrderBy(sql.Asc(sql.Collate(sql.Col("name"), "C"))),
		`SELECT "id" FROM "t" ORDER BY "name" COLLATE "C"`)
}
