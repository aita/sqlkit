package meta

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"strings"

	"github.com/aita/sqlkit/sql"
)

// TableKey is a stable identity for a table: its schema-qualified name plus a
// checksum over its structure (columns, primary key, indexes, foreign keys,
// checks). Two Table values with the same structure produce the same key, even
// when assembled by independent packages, so consumers — notably the uow
// package's Unit of Work — can reconcile bindings of the same physical table.
// The same name with a different structure yields a different checksum,
// surfacing schema drift instead of silently merging incompatible tables.
type TableKey struct {
	Schema   string
	Name     string
	Checksum string // hex-encoded SHA-256 of the canonical structural encoding
}

// QualifiedName returns the table name with an optional schema prefix.
func (k TableKey) QualifiedName() string {
	if k.Schema == "" {
		return k.Name
	}
	return k.Schema + "." + k.Name
}

// String renders the key as "schema.name#<short-checksum>" for diagnostics.
func (k TableKey) String() string {
	short := k.Checksum
	if len(short) > 8 {
		short = short[:8]
	}
	return k.QualifiedName() + "#" + short
}

// checksumDialect is the canonical dialect used to render DEFAULT and CHECK
// expressions for the checksum. The choice is arbitrary but must be fixed so
// the digest is stable across processes; PostgreSQL is the reference render.
var checksumDialect sql.Dialect = sql.PostgreSQLDialect{}

// checksumEngines are the dialects whose resolved engine types feed the
// checksum, in fixed order so the encoding is deterministic regardless of the
// target a binding is generated for.
var checksumEngines = []string{"postgres", "mysql"}

// Key computes the table's TableKey. The checksum is a SHA-256 over a
// canonical, length-framed encoding of the table's structure: schema and name;
// each column's name, nullability, default flag, primary-key flag, resolved
// engine type, and default expression; then indexes, foreign keys (including
// their ON DELETE / ON UPDATE referential actions), and checks.
// Every string is length-prefixed and every list is count-prefixed so that no
// regrouping of fields can collide. The encoding reads only value fields, so it
// is identical for any two Table values describing the same structure.
func (t Table) Key() TableKey {
	h := sha256.New()
	f := framer{h}

	f.str(t.Schema.Name)
	f.str(t.Name)

	f.count(len(t.Columns))
	for _, c := range t.Columns {
		f.str(c.Name)
		f.flag(c.Nullable)
		f.flag(c.HasDefault)
		f.flag(c.PrimaryKey)
		f.str(encodeType(c.Type))
		f.str(encodeExpr(c.DefaultExpr))
	}

	f.count(len(t.Indexes))
	for _, ix := range t.Indexes {
		f.str(ix.Name)
		f.flag(ix.Unique)
		f.strs(ix.Columns)
	}

	f.count(len(t.ForeignKeys))
	for _, fk := range t.ForeignKeys {
		f.str(fk.Name)
		f.strs(fk.Columns)
		f.str(fk.RefSchema.Name)
		f.str(fk.RefTable)
		f.strs(fk.RefColumns)
		f.str(string(fk.OnDelete))
		f.str(string(fk.OnUpdate))
	}

	f.count(len(t.Checks))
	for _, ck := range t.Checks {
		f.str(ck.Name)
		f.str(encodeExpr(ck.Expression))
	}

	return TableKey{
		Schema:   t.Schema.Name,
		Name:     t.Name,
		Checksum: hex.EncodeToString(h.Sum(nil)),
	}
}

// encodeType renders a portable type as its resolved engine types across the
// fixed checksum dialects, so two types that resolve identically everywhere
// hash equal. A nil type (a column declaring none) encodes as empty.
func encodeType(t Type) string {
	if t == nil {
		return ""
	}
	var sb strings.Builder
	for _, dialect := range checksumEngines {
		eng, ok := t.Engine(dialect)
		if !ok {
			continue
		}
		fmt.Fprintf(&sb, "%s=%s(%d,%d,%d);", dialect, eng.Name, eng.Length, eng.Precision, eng.Scale)
	}
	return sb.String()
}

// encodeExpr renders a static DEFAULT/CHECK expression to canonical SQL for the
// checksum. Default and check expressions bind no values, so CompileStatic
// succeeds; if it ever cannot, the expression's type stands in so distinct
// expressions still differ rather than collapsing to empty.
func encodeExpr(expr sql.Expression) string {
	if expr == nil {
		return ""
	}
	out, err := sql.CompileStatic(checksumDialect, expr)
	if err != nil {
		return fmt.Sprintf("!%T", expr)
	}
	return out
}

// framer writes length- and count-prefixed fields into a hash so the structural
// encoding is unambiguous: ["ab","c"] and ["a","bc"] hash differently.
type framer struct{ h hash.Hash }

func (f framer) str(s string) {
	f.count(len(s))
	io.WriteString(f.h, s)
}

func (f framer) strs(ss []string) {
	f.count(len(ss))
	for _, s := range ss {
		f.str(s)
	}
}

func (f framer) count(n int) {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], uint64(n))
	f.h.Write(b[:])
}

func (f framer) flag(b bool) {
	if b {
		f.h.Write([]byte{1})
	} else {
		f.h.Write([]byte{0})
	}
}

// ErrTableConflict reports two tables that share a schema-qualified name but
// have a different structure (checksum) within one IdentityMap. It signals
// schema drift between independently built or generated tables of the same
// name.
var ErrTableConflict = errors.New("meta: conflicting tables with the same name but different structure")

// IdentityMap indexes tables by TableKey. It is built from a Metadata — the
// source of truth assembled from a decl catalog — and is the bridge a Unit of
// Work uses to validate table bindings against the catalog: a binding's
// schema, name, and checksum form a TableKey that must resolve here.
type IdentityMap struct {
	byKey  map[TableKey]Table
	byName map[string]TableKey
}

// Identities builds an IdentityMap from the metadata. It returns
// ErrTableConflict when two tables share a schema-qualified name but differ in
// structure; tables that repeat with the same identity are folded together.
func (m Metadata) Identities() (*IdentityMap, error) {
	im := &IdentityMap{
		byKey:  make(map[TableKey]Table, len(m.Tables)),
		byName: make(map[string]TableKey, len(m.Tables)),
	}
	for _, t := range m.Tables {
		if err := im.Add(t); err != nil {
			return nil, err
		}
	}
	return im, nil
}

// Add registers a table, folding a repeat of the same identity and reporting
// ErrTableConflict on a same-name, different-structure clash.
func (im *IdentityMap) Add(t Table) error {
	key := t.Key()
	nk := nameIndexKey(t.Schema.Name, t.Name)
	if existing, ok := im.byName[nk]; ok {
		if existing.Checksum != key.Checksum {
			return fmt.Errorf("%w: %s vs %s", ErrTableConflict, existing, key)
		}
		return nil
	}
	im.byKey[key] = t
	im.byName[nk] = key
	return nil
}

// Lookup returns the table registered under key.
func (im *IdentityMap) Lookup(key TableKey) (Table, bool) {
	t, ok := im.byKey[key]
	return t, ok
}

// KeyForName returns the registered key for a schema-qualified name, if any.
func (im *IdentityMap) KeyForName(schema, name string) (TableKey, bool) {
	k, ok := im.byName[nameIndexKey(schema, name)]
	return k, ok
}

// Len reports how many distinct tables are registered.
func (im *IdentityMap) Len() int { return len(im.byKey) }

// nameIndexKey matches the schema\x00name convention used by decl and the
// migrate diff engine.
func nameIndexKey(schema, name string) string { return schema + "\x00" + name }
