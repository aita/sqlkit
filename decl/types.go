package decl

import (
	"reflect"
	"sync"

	"github.com/aita/sqlkit/meta"
)

// DataType is a portable column type implementing meta.Type. It names a
// canonical type kind (e.g. "bigint", "varchar") plus any length/precision
// modifiers; the per-dialect engine type name is resolved through the type
// registry when a generator picks a dialect, so meta stays dialect-free and new
// dialects (sqlite, ...) are added by registering, not by changing this type.
//
// A DataType fixes the DB type — Type names a registered kind (or, for an
// unregistered name, one used verbatim on every dialect) and TypeFor pins a
// distinct engine name per dialect inline — and can additionally pin the Go type
// the code generator maps it to with Go/GoFor, so a custom column type is fully
// specified at the schema source.
type DataType struct {
	// kind is the canonical type kind, resolved through the registry; when no
	// names map is set and the kind is unregistered it is used verbatim on every
	// dialect (the Type escape hatch).
	kind string
	// names pins the engine type name per dialect inline (TypeFor), overriding
	// the registry; a dialect missing from it makes the type unavailable there.
	names map[string]string
	// length/precision/scale carry the type modifiers, e.g. varchar(length) or
	// decimal(precision, scale).
	length    int
	precision int
	scale     int
	// goTypes pins the Go type per dialect ("" key = every dialect), set by
	// Go/GoFor and read back through meta.GoTyped; nil when unpinned, so the
	// generator falls back to its built-in type tables.
	goTypes map[string]meta.GoType
	// array marks a one-dimensional array of the element type (PostgreSQL only),
	// set by Array. The kind/names/length/precision/scale/goTypes describe the
	// element; Engine resolves the element name and flags Array.
	array bool
}

// dbTypeRegistry maps a canonical type kind to its engine type name per dialect.
// RegisterType writes it and DataType.Engine reads it; built-in standard types
// are registered in init. It is guarded by a mutex so registration from
// different packages' init functions is safe.
var (
	dbTypeMu       sync.RWMutex
	dbTypeRegistry = map[string]map[string]string{}
)

// RegisterType registers (or extends) the engine type names a type kind resolves
// to per dialect, so a custom type or a new dialect is added in one place rather
// than by changing the DSL. names is keyed by dialect ("postgres", "mysql",
// "sqlite", ...); entries merge into any existing registration for the kind, so
// a later call can add a dialect to a built-in kind. After registering, declare
// columns of the kind with Type(kind):
//
//	decl.RegisterType("uuid", map[string]string{"sqlite": "text"})
//	decl.RegisterType("geometry", map[string]string{
//		"postgres": "geometry", "mysql": "geometry", "sqlite": "blob",
//	})
//	decl.Column("shape", decl.Type("geometry"))
func RegisterType(kind string, names map[string]string) {
	dbTypeMu.Lock()
	defer dbTypeMu.Unlock()
	existing := dbTypeRegistry[kind]
	if existing == nil {
		existing = make(map[string]string, len(names))
		dbTypeRegistry[kind] = existing
	}
	for dialect, name := range names {
		existing[dialect] = name
	}
}

// lookupDBType returns the registered per-dialect names for a kind.
func lookupDBType(kind string) (map[string]string, bool) {
	dbTypeMu.RLock()
	defer dbTypeMu.RUnlock()
	m, ok := dbTypeRegistry[kind]
	return m, ok
}

func init() {
	for kind, names := range map[string]map[string]string{
		"bool":      {"postgres": "bool", "mysql": "boolean", "sqlite": "boolean"},
		"smallint":  {"postgres": "int2", "mysql": "smallint", "sqlite": "integer"},
		"int":       {"postgres": "int4", "mysql": "int", "sqlite": "integer"},
		"bigint":    {"postgres": "int8", "mysql": "bigint", "sqlite": "integer"},
		"float":     {"postgres": "float4", "mysql": "float", "sqlite": "real"},
		"double":    {"postgres": "float8", "mysql": "double", "sqlite": "real"},
		"text":      {"postgres": "text", "mysql": "text", "sqlite": "text"},
		"bytes":     {"postgres": "bytea", "mysql": "blob", "sqlite": "blob"},
		"json":      {"postgres": "jsonb", "mysql": "json", "sqlite": "text"},
		"uuid":      {"postgres": "uuid", "mysql": "char", "sqlite": "text"},
		"date":      {"postgres": "date", "mysql": "date", "sqlite": "text"},
		"time":      {"postgres": "time", "mysql": "time", "sqlite": "text"},
		"timestamp": {"postgres": "timestamptz", "mysql": "datetime", "sqlite": "datetime"},
		"varchar":   {"postgres": "varchar", "mysql": "varchar", "sqlite": "text"},
		"char":      {"postgres": "bpchar", "mysql": "char", "sqlite": "text"},
		"decimal":   {"postgres": "numeric", "mysql": "decimal", "sqlite": "numeric"},
	} {
		RegisterType(kind, names)
	}
}

// Engine implements meta.Type.
func (t DataType) Engine(dialect string) (meta.Engine, bool) {
	name, ok := t.engineName(dialect)
	if !ok || name == "" {
		return meta.Engine{}, false
	}
	if t.array && !arrayDialect(dialect) {
		// Arrays are a PostgreSQL feature; the column is unavailable elsewhere, so
		// targeting another dialect reports the type as missing rather than
		// rendering an invalid "elem[]".
		return meta.Engine{}, false
	}
	return meta.Engine{Name: name, Length: t.length, Precision: t.precision, Scale: t.scale, Array: t.array}, true
}

// arrayDialect reports whether the dialect supports array column types. Only
// PostgreSQL does; the introspection and registry both spell it "postgres".
func arrayDialect(dialect string) bool { return dialect == "postgres" }

// engineName resolves the engine type name for the dialect: an inline names pin
// first, then the registry, then the bare kind verbatim for an unregistered
// name (the Type escape hatch).
func (t DataType) engineName(dialect string) (string, bool) {
	if t.names != nil {
		name, ok := t.names[dialect]
		return name, ok
	}
	if reg, ok := lookupDBType(t.kind); ok {
		name, ok := reg[dialect]
		return name, ok
	}
	return t.kind, t.kind != ""
}

// GoType implements meta.GoTyped: it reports the Go type pinned for the dialect
// (with Go/GoFor), so codegen's DefaultTypeMapper maps the column to it without
// a separate override. ok is false when no type is pinned, falling back to the
// generator's tables.
func (t DataType) GoType(dialect string) (meta.GoType, bool) {
	if t.goTypes == nil {
		return meta.GoType{}, false
	}
	if gt, ok := t.goTypes[dialect]; ok {
		return gt, true
	}
	gt, ok := t.goTypes[""]
	return gt, ok
}

// Go pins the Go type the code generator maps this column type to on every
// dialect, returning a copy of the type. Build the meta.GoType with the GoType
// or GoTypeOf helpers, e.g. to fix the mapping of a custom DB type at the schema
// source:
//
//	decl.Type("citext").Go(decl.GoTypeOf[string]())
//	decl.Type("uuid").Go(decl.GoTypeOf[uuid.UUID]())
//
// For a nullable column the generator still applies the pointer; pin the bare
// type. GoFor pins a different type per dialect.
func (t DataType) Go(gt meta.GoType) DataType {
	return t.GoFor("", gt)
}

// GoFor pins the Go type for a single dialect ("postgres", "mysql", ...); an
// empty dialect pins the default used when a dialect has no specific pin. Chain
// calls to pin dialects distinctly. It returns a copy of the type.
func (t DataType) GoFor(dialect string, gt meta.GoType) DataType {
	pinned := make(map[string]meta.GoType, len(t.goTypes)+1)
	for k, v := range t.goTypes {
		pinned[k] = v
	}
	pinned[dialect] = gt
	t.goTypes = pinned
	return t
}

// GoType derives a meta.GoType from a Go type for pinning a column's Go type
// (DataType.Go): the import path comes from the type's package and the name from
// its source spelling (e.g. "uuid.UUID", "string"). GoTypeOf is the generic,
// reflection-free spelling of the common case.
//
//	decl.GoType(reflect.TypeOf(uuid.UUID{})) // {ImportPath: "github.com/google/uuid", Name: "uuid.UUID"}
func GoType(t reflect.Type) meta.GoType {
	return meta.GoType{ImportPath: t.PkgPath(), Name: t.String()}
}

// GoTypeOf derives a meta.GoType from the type parameter T, e.g.
// decl.GoTypeOf[uuid.UUID](). It is GoType applied to reflect.TypeOf a T.
func GoTypeOf[T any]() meta.GoType {
	return GoType(reflect.TypeOf((*T)(nil)).Elem())
}

// Portable column types. Each names a registered kind, resolved to the engine
// name of the chosen dialect by the generators.
var (
	Bool     = Type("bool")
	SmallInt = Type("smallint")
	Int      = Type("int")
	BigInt   = Type("bigint")
	Float    = Type("float")
	Double   = Type("double")
	Text     = Type("text")
	Bytes    = Type("bytes")
	JSON     = Type("json")
	UUID     = DataType{kind: "uuid", length: 36}
	Date     = Type("date")
	Time     = Type("time")
	// Timestamp is a point in time: timestamptz on PostgreSQL, datetime on
	// MySQL. Use Type("timestamp") for the engines' bare timestamp types.
	Timestamp = Type("timestamp")
)

// VarChar is a variable-length string of at most n characters.
func VarChar(n int) DataType {
	return DataType{kind: "varchar", length: n}
}

// Char is a fixed-length string of n characters.
func Char(n int) DataType {
	return DataType{kind: "char", length: n}
}

// Decimal is an exact numeric with the given precision and scale.
func Decimal(precision, scale int) DataType {
	return DataType{kind: "decimal", precision: precision, scale: scale}
}

// Array declares a one-dimensional array of the element type, e.g.
// Array(Text) for text[] or Array(Int) for int4[]. Arrays are a PostgreSQL
// feature: the column is unavailable on other dialects (the generators report it
// as a missing type when targeting them). The code generator maps the column to
// a Go slice of the element's type ([]string, []int32, ...), honoring any Go
// type pinned on the element with Go/GoFor. Nullability is irrelevant to the Go
// type — a slice is already nilable — so a nullable array stays a slice, not a
// pointer.
func Array(elem DataType) DataType {
	elem.array = true
	return elem
}

// Type declares a column type by its kind name. A registered kind (the built-in
// standard types, or one added with RegisterType) resolves to its engine name
// per dialect; an unregistered name is used verbatim on every dialect (e.g.
// Type("citext")). For a custom type the generator's tables don't know, pin its
// Go type with Go/GoFor (e.g. Type("citext").Go(decl.GoTypeOf[string]())).
func Type(name string) DataType {
	return DataType{kind: name}
}

// TypeFor declares a column type with a distinct, fixed engine type name per
// dialect inline, keyed by dialect name ("postgres", "mysql", "sqlite", ...). A
// dialect absent from names makes the type unavailable there, which the
// generators report as an error when targeting it. For a reusable type, prefer
// RegisterType plus Type. Chain Go/GoFor to also pin the Go type.
//
//	decl.TypeFor(map[string]string{"postgres": "uuid", "mysql": "char"})
func TypeFor(names map[string]string) DataType {
	pinned := make(map[string]string, len(names))
	for dialect, name := range names {
		pinned[dialect] = name
	}
	return DataType{names: pinned}
}
