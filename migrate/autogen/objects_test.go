package autogen

import (
	"bytes"
	"strings"
	"testing"

	"github.com/aita/sqlkit/decl"
	"github.com/aita/sqlkit/meta"
)

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
