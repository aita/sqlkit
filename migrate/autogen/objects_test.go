package autogen

import (
	"bytes"
	"strings"
	"testing"

	"github.com/aita/sqlkit/decl"
	"github.com/aita/sqlkit/meta"
	"github.com/aita/sqlkit/sql"
)

func i64(n int64) *int64 { return &n }
func boolp(b bool) *bool { return &b }

func TestRenderObjects(t *testing.T) {
	target := func() *decl.Catalog {
		c := decl.NewCatalog(decl.DefaultSchema("public"))
		c.Extension("pg_trgm")
		c.Enum("mood", "happy", "sad")
		c.Sequence("order_no", decl.SequenceStart(1000), decl.SequenceIncrement(1))
		return c
	}
	res := render(t, meta.Metadata{}, build(t, target()), "postgres", Renames{})

	for _, want := range []string{
		`op.CreateExtension("pg_trgm")`,
		`op.CreateEnum("mood", "happy", "sad")`,
		`op.CreateSequence("order_no", func(s sql.CreateSequenceBuilder) sql.CreateSequenceBuilder {`,
		`.Increment(1).Start(1000)`,
	} {
		if !strings.Contains(res.Up, want) {
			t.Fatalf("Up missing %q in:\n%s", want, res.Up)
		}
	}
	// The sequence configure closure references the sql package.
	if !res.UsesSQL {
		t.Error("expected UsesSQL for the sequence configure closure")
	}
	// Down reverses to drops.
	for _, want := range []string{`op.DropExtension("pg_trgm")`, `op.DropEnum("mood")`, `op.DropSequence("order_no")`} {
		if !strings.Contains(res.Down, want) {
			t.Fatalf("Down missing %q in:\n%s", want, res.Down)
		}
	}
}

// TestRenderAppendedEnum covers the common enum change in the codegen-ops path:
// appending a label renders op.AddEnumValue (one per added label, in order), not
// op.DropEnum + op.CreateEnum. The reverse direction (Down) removes the label,
// which is not a pure append, so it falls back to drop+create.
func TestRenderAppendedEnum(t *testing.T) {
	source := meta.Metadata{Enums: []meta.Enum{{Schema: "public", Name: "mood", Values: []string{"happy", "sad"}}}}
	target := meta.Metadata{Enums: []meta.Enum{{Schema: "public", Name: "mood", Values: []string{"happy", "sad", "neutral", "excited"}}}}
	res := render(t, source, target, "postgres", Renames{})

	for _, want := range []string{
		`op.AddEnumValue("mood", "neutral")`,
		`op.AddEnumValue("mood", "excited")`,
	} {
		if !strings.Contains(res.Up, want) {
			t.Fatalf("Up missing %q in:\n%s", want, res.Up)
		}
	}
	if strings.Contains(res.Up, "op.DropEnum") || strings.Contains(res.Up, "op.CreateEnum") {
		t.Fatalf("appended enum must not drop/create in Up:\n%s", res.Up)
	}
	// Down removes labels (not a pure append): drop + create.
	for _, want := range []string{`op.DropEnum("mood")`, `op.CreateEnum("mood", "happy", "sad")`} {
		if !strings.Contains(res.Down, want) {
			t.Fatalf("Down missing %q in:\n%s", want, res.Down)
		}
	}
	if strings.Contains(res.Down, "op.AddEnumValue") {
		t.Fatalf("Down must not ADD VALUE:\n%s", res.Down)
	}
}

// TestRenderGrants covers grant add/drop/change: a grant present only in the
// target is granted in Up and revoked in Down, and changing the privileges of an
// existing grant (same target key) revokes then re-grants.
func TestRenderGrants(t *testing.T) {
	source := meta.Metadata{Grants: []meta.Grant{
		{Privileges: []string{"SELECT"}, Objects: []string{"users"}, Grantees: []string{"reader"}},
	}}
	target := meta.Metadata{Grants: []meta.Grant{
		// Same target (TABLE users), changed privileges -> revoke + grant.
		{Privileges: []string{"SELECT", "INSERT"}, Objects: []string{"users"}, Grantees: []string{"reader"}, WithGrantOption: true},
		// New grant on a type, with explicit object type -> OnType in builder.
		{Privileges: []string{"USAGE"}, ObjectType: "SCHEMA", Objects: []string{"public"}, Grantees: []string{"app"}},
	}}
	res := render(t, source, target, "postgres", Renames{})
	assertValidGo(t, res.Up)
	assertValidGo(t, res.Down)

	for _, want := range []string{
		`op.Grant(sql.Grant("SELECT", "INSERT").On("users").To("reader").WithGrantOption())`,
		`op.Grant(sql.Grant("USAGE").OnType("SCHEMA", "public").To("app"))`,
		// The changed grant's old form is revoked first.
		`op.Revoke(sql.Revoke("SELECT").On("users").From("reader"))`,
	} {
		if !strings.Contains(res.Up, want) {
			t.Errorf("Up missing %q in:\n%s", want, res.Up)
		}
	}
	if !res.UsesSQL {
		t.Error("grant/revoke builders reference the sql package; expected UsesSQL")
	}
	// Down reverses: the new grants are revoked, the original is granted back.
	for _, want := range []string{
		`op.Revoke(sql.Revoke("USAGE").OnType("SCHEMA", "public").From("app"))`,
		`op.Grant(sql.Grant("SELECT").On("users").To("reader"))`,
	} {
		if !strings.Contains(res.Down, want) {
			t.Errorf("Down missing %q in:\n%s", want, res.Down)
		}
	}
}

// TestRenderMaterializedView covers add/drop/change of a materialized view,
// including the Columns and WithNoData render options.
func TestRenderMaterializedView(t *testing.T) {
	mk := func(body string, cols []string, noData bool) meta.Metadata {
		return meta.Metadata{MaterializedViews: []meta.MaterializedView{{
			Schema: "public", Name: "active", Query: sql.RawSQL(body), Columns: cols, WithNoData: noData,
		}}}
	}
	// Add: empty source, target has a view with explicit columns.
	res := render(t, meta.Metadata{}, mk("SELECT id FROM users", []string{"id"}, false), "postgres", Renames{})
	assertValidGo(t, res.Up)
	assertValidGo(t, res.Down)
	if !strings.Contains(res.Up, `op.CreateMaterializedView("active", sql.RawSQL("SELECT id FROM users"`) {
		t.Errorf("Up missing CreateMaterializedView:\n%s", res.Up)
	}
	if !strings.Contains(res.Up, `return s.Columns("id")`) {
		t.Errorf("Up missing Columns option:\n%s", res.Up)
	}
	if !strings.Contains(res.Down, `op.DropMaterializedView("active")`) {
		t.Errorf("Down missing DropMaterializedView:\n%s", res.Down)
	}
	if !res.UsesSQL {
		t.Error("matview create references the sql package; expected UsesSQL")
	}

	// WithNoData option (no columns) renders the WithNoData closure.
	res = render(t, meta.Metadata{}, mk("SELECT id FROM users", nil, true), "postgres", Renames{})
	if !strings.Contains(res.Up, `return s.WithNoData()`) {
		t.Errorf("Up missing WithNoData option:\n%s", res.Up)
	}

	// Change: a different body re-creates (drop old, create new) in Up.
	a := mk("SELECT id FROM users", nil, false)
	b := mk("SELECT id, name FROM users", nil, false)
	res = render(t, a, b, "postgres", Renames{})
	if !strings.Contains(res.Up, `op.DropMaterializedView("active")`) ||
		!strings.Contains(res.Up, `op.CreateMaterializedView("active", sql.RawSQL("SELECT id, name FROM users"`) {
		t.Errorf("changed matview should drop+create in Up:\n%s", res.Up)
	}
}

// TestRenderMatviewEqualGuards exercises matviewEqual's inequality branches so a
// view differing only by WithNoData/Columns/schema is treated as changed.
func TestRenderMatviewEqualGuards(t *testing.T) {
	base := meta.MaterializedView{Schema: "public", Name: "v", Query: sql.RawSQL("SELECT 1")}
	r := &renderer{dialect: mustDialect(t, "postgres")}
	cases := []struct {
		name string
		mut  func(meta.MaterializedView) meta.MaterializedView
	}{
		{"with-no-data", func(v meta.MaterializedView) meta.MaterializedView { v.WithNoData = true; return v }},
		{"columns", func(v meta.MaterializedView) meta.MaterializedView { v.Columns = []string{"x"}; return v }},
		{"schema", func(v meta.MaterializedView) meta.MaterializedView { v.Schema = "other"; return v }},
		{"nil-query", func(v meta.MaterializedView) meta.MaterializedView { v.Query = nil; return v }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if r.matviewEqual(base, tc.mut(base)) {
				t.Errorf("matviewEqual should report a %s difference", tc.name)
			}
		})
	}
	if !r.matviewEqual(base, base) {
		t.Error("matviewEqual should report identical views equal")
	}
}

// TestRenderSequenceFullOptions covers every sequence option closure and the
// NoCycle branch, then the bare (no-option) CreateSequence form.
func TestRenderSequenceFullOptions(t *testing.T) {
	full := meta.Metadata{Sequences: []meta.Sequence{{
		Schema: "public", Name: "s",
		Increment: i64(2), MinValue: i64(1), MaxValue: i64(100),
		Start: i64(10), Cache: i64(5), Cycle: boolp(false), OwnedBy: "users.id",
	}}}
	res := render(t, meta.Metadata{}, full, "postgres", Renames{})
	assertValidGo(t, res.Up)
	for _, want := range []string{
		`.Increment(2)`, `.MinValue(1)`, `.MaxValue(100)`, `.Start(10)`,
		`.Cache(5)`, `.NoCycle()`, `.OwnedBy("users.id")`,
	} {
		if !strings.Contains(res.Up, want) {
			t.Errorf("Up missing %q in:\n%s", want, res.Up)
		}
	}

	// Cycle(true) renders .Cycle().
	cyc := meta.Metadata{Sequences: []meta.Sequence{{Schema: "public", Name: "s", Cycle: boolp(true)}}}
	res = render(t, meta.Metadata{}, cyc, "postgres", Renames{})
	if !strings.Contains(res.Up, `.Cycle()`) {
		t.Errorf("Up missing .Cycle():\n%s", res.Up)
	}

	// No options at all -> the bare op.CreateSequence form (no closure, no sql import).
	bare := meta.Metadata{Sequences: []meta.Sequence{{Schema: "public", Name: "s"}}}
	res = render(t, meta.Metadata{}, bare, "postgres", Renames{})
	if !strings.Contains(res.Up, `op.CreateSequence("s")`) {
		t.Errorf("Up missing bare CreateSequence:\n%s", res.Up)
	}
	if strings.Contains(res.Up, "func(s sql.CreateSequenceBuilder)") {
		t.Errorf("bare sequence should not emit a configure closure:\n%s", res.Up)
	}
}

// TestRenderExtensionWithSchema covers the schema-qualified extension branch,
// which emits a configure closure and flags the sql import.
func TestRenderExtensionWithSchema(t *testing.T) {
	target := meta.Metadata{Extensions: []meta.Extension{{Name: "pg_trgm", Schema: "ext"}}}
	res := render(t, meta.Metadata{}, target, "postgres", Renames{})
	assertValidGo(t, res.Up)
	if !strings.Contains(res.Up, `return s.Schema("ext")`) {
		t.Errorf("Up missing extension Schema option:\n%s", res.Up)
	}
	if !res.UsesSQL {
		t.Error("schema-qualified extension references the sql package; expected UsesSQL")
	}
}

func TestSnapshotRoundTripObjects(t *testing.T) {
	c := decl.NewCatalog(decl.DefaultSchema("public"))
	c.Extension("pg_trgm", decl.ExtensionSchema("public"))
	c.Enum("mood", "happy", "sad")
	c.Sequence("order_no", decl.SequenceStart(5), decl.SequenceCycle(true))
	md := build(t, c)

	snap, err := Resolve(md, mustDialect(t, "postgres"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	var buf bytes.Buffer
	if err := snap.Encode(&buf); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := DecodeSnapshot(&buf)
	if err != nil {
		t.Fatalf("DecodeSnapshot: %v", err)
	}
	got := decoded.Metadata()

	if len(got.Extensions) != 1 || got.Extensions[0].Schema != "public" {
		t.Fatalf("extensions round-trip = %+v", got.Extensions)
	}
	if len(got.Enums) != 1 || !got.Enums[0].Equal(md.Enums[0]) {
		t.Fatalf("enums round-trip = %+v", got.Enums)
	}
	if len(got.Sequences) != 1 || !got.Sequences[0].Equal(md.Sequences[0]) {
		t.Fatalf("sequences round-trip = %+v want %+v", got.Sequences, md.Sequences)
	}
}
