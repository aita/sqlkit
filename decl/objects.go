package decl

import "github.com/aita/sqlkit/meta"

// This file adds declarative, non-table schema objects to the catalog DSL:
// extensions, enum types, and sequences (PostgreSQL). They build into the
// meta.Metadata that Build returns, so CreateSQL/DropSQL emit them in dependency
// order alongside the tables. Each is declared on the catalog's default schema,
// or on a Schema handle to qualify it.

// objectKey namespaces a declared object for duplicate detection.
func objectKey(schema, kind, name string) string {
	return schema + "\x00" + kind + "\x00" + name
}

func (d *Catalog) declareObject(schema, kind, name string) bool {
	if name == "" {
		d.fail("%s requires a name", kind)
		return false
	}
	key := objectKey(schema, kind, name)
	if d.objectNames[key] {
		d.fail("%s %q is declared twice in schema %q", kind, name, schema)
		return false
	}
	d.objectNames[key] = true
	return true
}

// Extension declares a database extension (PostgreSQL CREATE EXTENSION). The
// extension is created idempotently (IF NOT EXISTS). Extensions are not
// schema-scoped objects, so they are declared on the catalog rather than a Schema
// handle; pass ExtensionSchema to install the extension's objects into a schema.
func (d *Catalog) Extension(name string, opts ...ExtensionOption) {
	if !d.declareObject("", "extension", name) {
		return
	}
	ext := meta.Extension{Name: name}
	for _, opt := range opts {
		opt(&ext)
	}
	d.extensions = append(d.extensions, ext)
}

// ExtensionOption customizes an extension declared with Extension.
type ExtensionOption func(*meta.Extension)

// ExtensionSchema installs the extension's objects into the named schema
// (CREATE EXTENSION ... WITH SCHEMA name).
func ExtensionSchema(schema string) ExtensionOption {
	return func(e *meta.Extension) { e.Schema = schema }
}

// Enum declares an enum type in the catalog's default schema. Use
// Catalog.Schema("other").Enum(...) to place it in a different schema.
func (d *Catalog) Enum(name string, values ...string) {
	d.Schema(d.defaultSchema).Enum(name, values...)
}

// Enum declares an enum type (PostgreSQL CREATE TYPE ... AS ENUM) in the schema.
func (s *Schema) Enum(name string, values ...string) {
	d := s.catalog
	if !d.declareObject(s.name, "enum", name) {
		return
	}
	if len(values) == 0 {
		d.fail("enum %q declares no values", name)
		return
	}
	d.schemaNamesAdd(s.name)
	d.enums = append(d.enums, meta.Enum{Schema: s.name, Name: name, Values: values})
}

// Sequence declares a sequence in the catalog's default schema. Use
// Catalog.Schema("other").Sequence(...) to place it in a different schema.
func (d *Catalog) Sequence(name string, opts ...SequenceOption) {
	d.Schema(d.defaultSchema).Sequence(name, opts...)
}

// Sequence declares a sequence (PostgreSQL CREATE SEQUENCE) in the schema. Unset
// options take the engine default.
func (s *Schema) Sequence(name string, opts ...SequenceOption) {
	d := s.catalog
	if !d.declareObject(s.name, "sequence", name) {
		return
	}
	seq := meta.Sequence{Schema: s.name, Name: name}
	for _, opt := range opts {
		opt(&seq)
	}
	d.schemaNamesAdd(s.name)
	d.sequences = append(d.sequences, seq)
}

// SequenceOption customizes a sequence declared with Sequence.
type SequenceOption func(*meta.Sequence)

func int64p(n int64) *int64 { return &n }

// SequenceStart sets START WITH.
func SequenceStart(n int64) SequenceOption {
	return func(s *meta.Sequence) { s.Start = int64p(n) }
}

// SequenceIncrement sets INCREMENT BY.
func SequenceIncrement(n int64) SequenceOption {
	return func(s *meta.Sequence) { s.Increment = int64p(n) }
}

// SequenceMinValue sets MINVALUE.
func SequenceMinValue(n int64) SequenceOption {
	return func(s *meta.Sequence) { s.MinValue = int64p(n) }
}

// SequenceMaxValue sets MAXVALUE.
func SequenceMaxValue(n int64) SequenceOption {
	return func(s *meta.Sequence) { s.MaxValue = int64p(n) }
}

// SequenceCache sets CACHE.
func SequenceCache(n int64) SequenceOption {
	return func(s *meta.Sequence) { s.Cache = int64p(n) }
}

// SequenceCycle sets CYCLE (true) or NO CYCLE (false).
func SequenceCycle(cycle bool) SequenceOption {
	return func(s *meta.Sequence) { s.Cycle = &cycle }
}

// SequenceOwnedBy sets OWNED BY a "table.column", tying the sequence's lifetime
// to that column, or "NONE" to detach it.
func SequenceOwnedBy(column string) SequenceOption {
	return func(s *meta.Sequence) { s.OwnedBy = column }
}
