// Package autogen generates a migration by diffing a source schema against a
// target schema. The target is the project's decl catalog (the desired state);
// the source is where the database is migrating from — a live database read by
// the introspection, or the catalog as it stood at an earlier git revision. Render
// turns the difference into a migration's Up/Down bodies written in the decl
// Operator DSL (op.CreateTable, op.AddColumn, …).
//
// Because the target catalog and a git-revision catalog are different compiled
// programs, a schema must cross a process boundary: Snapshot is a JSON-
// serializable, dialect-resolved form of meta.Metadata. Resolve flattens a
// metadata's portable column types and default/check expressions for one dialect
// (the same simplification the diff engine makes), and Snapshot.Metadata rebuilds
// a meta.Metadata the diff and renderer consume.
package autogen

import (
	"encoding/gob"
	"fmt"
	"io"

	"github.com/aita/sqlkit/meta"
	"github.com/aita/sqlkit/sql"
)

// Snapshot is a dialect-resolved, gob-serializable schema, the form a schema
// takes when it crosses the shim→command process boundary. Both ends are Go, so
// the snapshot is encoded with encoding/gob.
type Snapshot struct {
	Dialect    string
	Tables     []SnapshotTable
	Extensions []SnapshotExtension
	Enums      []SnapshotEnum
	Sequences  []SnapshotSequence
}

// SnapshotExtension is one database extension.
type SnapshotExtension struct {
	Name   string
	Schema string
}

// SnapshotEnum is one enum type and its labels.
type SnapshotEnum struct {
	Schema string
	Name   string
	Values []string
}

// SnapshotSequence is one sequence. The numeric options are pointers so an unset
// option round-trips as unset rather than zero.
type SnapshotSequence struct {
	Schema    string
	Name      string
	Increment *int64
	Start     *int64
	MinValue  *int64
	MaxValue  *int64
	Cache     *int64
	Cycle     *bool
	OwnedBy   string
}

// Encode writes the snapshot to w as gob — the shim end of the boundary.
func (s Snapshot) Encode(w io.Writer) error {
	return gob.NewEncoder(w).Encode(s)
}

// DecodeSnapshot reads a gob-encoded snapshot from r — the command end.
func DecodeSnapshot(r io.Reader) (Snapshot, error) {
	var s Snapshot
	if err := gob.NewDecoder(r).Decode(&s); err != nil {
		return Snapshot{}, fmt.Errorf("autogen: decoding schema snapshot: %w", err)
	}
	return s, nil
}

// SnapshotTable is one table of a Snapshot.
type SnapshotTable struct {
	Schema      string
	Name        string
	Comment     string
	Columns     []SnapshotColumn
	ForeignKeys []SnapshotFK
	Indexes     []SnapshotIndex
	Checks      []SnapshotCheck
}

// SnapshotColumn is one column; Type is resolved to its engine type and Default
// is the rendered default SQL text ("" when none, or for serial/auto-increment
// columns whose default carries no expression).
type SnapshotColumn struct {
	Name       string
	Type       SnapshotType
	Nullable   bool
	HasDefault bool
	Default    string
	PrimaryKey bool
	Comment    string
}

// SnapshotType is a resolved engine type and its modifiers.
type SnapshotType struct {
	Name      string
	Length    int
	Precision int
	Scale     int
}

// SnapshotFK is one foreign-key constraint with its referential actions.
type SnapshotFK struct {
	Name       string
	Columns    []string
	RefSchema  string
	RefTable   string
	RefColumns []string
	OnDelete   string
	OnUpdate   string
}

// SnapshotIndex is one secondary index.
type SnapshotIndex struct {
	Name    string
	Columns []string
	Unique  bool
}

// SnapshotCheck is one CHECK constraint with its rendered expression text.
type SnapshotCheck struct {
	Name       string
	Expression string
}

// Resolve flattens a meta.Metadata to a Snapshot for dialect d: column types
// resolve to engine types and default/check expressions render to SQL text. It
// errors if a column has no type on the dialect or an expression cannot be
// compiled.
func Resolve(md meta.Metadata, d sql.Dialect) (Snapshot, error) {
	snap := Snapshot{Dialect: d.Name()}
	for _, t := range md.Tables {
		st := SnapshotTable{Schema: t.Schema.Name, Name: t.Name, Comment: t.Comment}
		for _, c := range t.Columns {
			if c.Type == nil {
				return Snapshot{}, fmt.Errorf("autogen: table %q column %q has no type", t.Name, c.Name)
			}
			eng, ok := c.Type.Engine(d.Name())
			if !ok {
				return Snapshot{}, fmt.Errorf("autogen: table %q column %q has no type on dialect %s", t.Name, c.Name, d.Name())
			}
			sc := SnapshotColumn{
				Name:       c.Name,
				Type:       SnapshotType{Name: eng.Name, Length: eng.Length, Precision: eng.Precision, Scale: eng.Scale},
				Nullable:   c.Nullable,
				HasDefault: c.HasDefault,
				PrimaryKey: c.PrimaryKey,
				Comment:    c.Comment,
			}
			if c.DefaultExpr != nil {
				text, err := sql.CompileStatic(d, c.DefaultExpr)
				if err != nil {
					return Snapshot{}, fmt.Errorf("autogen: table %q column %q default: %w", t.Name, c.Name, err)
				}
				sc.Default = text
			}
			st.Columns = append(st.Columns, sc)
		}
		for _, fk := range t.ForeignKeys {
			st.ForeignKeys = append(st.ForeignKeys, SnapshotFK{
				Name:       fk.Name,
				Columns:    fk.Columns,
				RefSchema:  fk.RefSchema.Name,
				RefTable:   fk.RefTable,
				RefColumns: fk.RefColumns,
				OnDelete:   string(fk.OnDelete),
				OnUpdate:   string(fk.OnUpdate),
			})
		}
		for _, ix := range t.Indexes {
			st.Indexes = append(st.Indexes, SnapshotIndex{Name: ix.Name, Columns: ix.Columns, Unique: ix.Unique})
		}
		for _, ck := range t.Checks {
			text, err := sql.CompileStatic(d, ck.Expression)
			if err != nil {
				return Snapshot{}, fmt.Errorf("autogen: table %q check %q: %w", t.Name, ck.Name, err)
			}
			st.Checks = append(st.Checks, SnapshotCheck{Name: ck.Name, Expression: text})
		}
		snap.Tables = append(snap.Tables, st)
	}
	// Non-table objects carry no portable types or expressions, so they copy
	// across unchanged.
	for _, e := range md.Extensions {
		snap.Extensions = append(snap.Extensions, SnapshotExtension{Name: e.Name, Schema: e.Schema})
	}
	for _, e := range md.Enums {
		snap.Enums = append(snap.Enums, SnapshotEnum{Schema: e.Schema, Name: e.Name, Values: e.Values})
	}
	for _, s := range md.Sequences {
		snap.Sequences = append(snap.Sequences, SnapshotSequence{
			Schema: s.Schema, Name: s.Name,
			Increment: s.Increment, Start: s.Start, MinValue: s.MinValue,
			MaxValue: s.MaxValue, Cache: s.Cache, Cycle: s.Cycle, OwnedBy: s.OwnedBy,
		})
	}
	return snap, nil
}

// Metadata rebuilds a meta.Metadata from the snapshot. Column types become
// single-dialect meta.Of types and default/check expressions become sql.Raw
// fragments — the form the diff engine and renderer consume.
func (s Snapshot) Metadata() meta.Metadata {
	var md meta.Metadata
	for _, st := range s.Tables {
		t := meta.Table{Schema: meta.Schema{Name: st.Schema}, Name: st.Name, Comment: st.Comment}
		for _, sc := range st.Columns {
			c := meta.Column{
				Name:       sc.Name,
				Type:       meta.Of(s.Dialect, sc.Type.Name, sc.Type.Length, sc.Type.Precision, sc.Type.Scale),
				Nullable:   sc.Nullable,
				HasDefault: sc.HasDefault,
				PrimaryKey: sc.PrimaryKey,
				Comment:    sc.Comment,
			}
			if sc.Default != "" {
				c.DefaultExpr = sql.Raw(sc.Default)
			}
			t.Columns = append(t.Columns, c)
		}
		for _, fk := range st.ForeignKeys {
			t.ForeignKeys = append(t.ForeignKeys, meta.ForeignKey{
				Name:       fk.Name,
				Columns:    fk.Columns,
				RefSchema:  meta.Schema{Name: fk.RefSchema},
				RefTable:   fk.RefTable,
				RefColumns: fk.RefColumns,
				OnDelete:   meta.ReferentialAction(fk.OnDelete),
				OnUpdate:   meta.ReferentialAction(fk.OnUpdate),
			})
		}
		for _, ix := range st.Indexes {
			t.Indexes = append(t.Indexes, meta.Index{Name: ix.Name, Columns: ix.Columns, Unique: ix.Unique})
		}
		for _, ck := range st.Checks {
			t.Checks = append(t.Checks, meta.Check{Name: ck.Name, Expression: sql.Raw(ck.Expression)})
		}
		md.Tables = append(md.Tables, t)
	}
	for _, e := range s.Extensions {
		md.Extensions = append(md.Extensions, meta.Extension{Name: e.Name, Schema: e.Schema})
	}
	for _, e := range s.Enums {
		md.Enums = append(md.Enums, meta.Enum{Schema: e.Schema, Name: e.Name, Values: e.Values})
	}
	for _, sq := range s.Sequences {
		md.Sequences = append(md.Sequences, meta.Sequence{
			Schema: sq.Schema, Name: sq.Name,
			Increment: sq.Increment, Start: sq.Start, MinValue: sq.MinValue,
			MaxValue: sq.MaxValue, Cache: sq.Cache, Cycle: sq.Cycle, OwnedBy: sq.OwnedBy,
		})
	}
	return md
}
