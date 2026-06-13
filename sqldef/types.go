package sqldef

import "github.com/aita/sqlkit/meta"

// DataType is a portable column type implementing meta.Type: it knows the
// engine type name on each dialect plus any length/precision modifiers, and
// resolves to one when a generator picks a dialect. The DSL's type
// constructors return it; sqldef owns the per-dialect knowledge so meta stays
// dialect-free.
type DataType struct {
	postgres  string
	mysql     string
	length    int
	precision int
	scale     int
}

// Engine implements meta.Type.
func (t DataType) Engine(dialect string) (meta.Engine, bool) {
	name := t.postgres
	if dialect == "mysql" {
		name = t.mysql
	}
	if name == "" {
		return meta.Engine{}, false
	}
	return meta.Engine{Name: name, Length: t.length, Precision: t.precision, Scale: t.scale}, true
}

// Portable column types. Each knows the engine type name of both dialects; the
// generators pick the one for the dialect they render.
var (
	Bool     = TypeFor("bool", "boolean")
	SmallInt = TypeFor("int2", "smallint")
	Int      = TypeFor("int4", "int")
	BigInt   = TypeFor("int8", "bigint")
	Float    = TypeFor("float4", "float")
	Double   = TypeFor("float8", "double")
	Text     = TypeFor("text", "text")
	Bytes    = TypeFor("bytea", "blob")
	JSON     = TypeFor("jsonb", "json")
	UUID     = DataType{postgres: "uuid", mysql: "char", length: 36}
	Date     = TypeFor("date", "date")
	Time     = TypeFor("time", "time")
	// Timestamp is a point in time: timestamptz on PostgreSQL, datetime on
	// MySQL. Use Type("timestamp") for the engines' bare timestamp types.
	Timestamp = TypeFor("timestamptz", "datetime")
)

// VarChar is a variable-length string of at most n characters.
func VarChar(n int) DataType {
	return DataType{postgres: "varchar", mysql: "varchar", length: n}
}

// Char is a fixed-length string of n characters.
func Char(n int) DataType {
	return DataType{postgres: "bpchar", mysql: "char", length: n}
}

// Decimal is an exact numeric with the given precision and scale.
func Decimal(precision, scale int) DataType {
	return DataType{postgres: "numeric", mysql: "decimal", precision: precision, scale: scale}
}

// Type declares a column type by its engine type name, used verbatim on every
// dialect (e.g. Type("citext")). The name must be one the code generator's
// type mapper knows, or be covered by a TypeMapper override.
func Type(name string) DataType {
	return TypeFor(name, name)
}

// TypeFor declares a column type with a distinct engine type name per
// dialect. An empty name makes the type unavailable on that dialect, which the
// generators report as an error when targeting it.
func TypeFor(postgres, mysql string) DataType {
	return DataType{postgres: postgres, mysql: mysql}
}
