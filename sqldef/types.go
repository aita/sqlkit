package sqldef

import "github.com/aita/sqlkit/meta"

// DataType is a portable column type in a design: the engine type name for
// each supported dialect plus optional length/precision modifiers. It is an
// alias for meta.Type — the value carried straight into the generated
// meta.Column — so the generators resolve it to a dialect at render time.
type DataType = meta.Type

// Portable column types. Each carries the engine type name of both dialects;
// the generators pick the one for the dialect they render.
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
	UUID     = DataType{Postgres: "uuid", MySQL: "char", Length: 36}
	Date     = TypeFor("date", "date")
	Time     = TypeFor("time", "time")
	// Timestamp is a point in time: timestamptz on PostgreSQL, datetime on
	// MySQL. Use Type("timestamp") for the engines' bare timestamp types.
	Timestamp = TypeFor("timestamptz", "datetime")
)

// VarChar is a variable-length string of at most n characters.
func VarChar(n int) DataType {
	return DataType{Postgres: "varchar", MySQL: "varchar", Length: n}
}

// Char is a fixed-length string of n characters.
func Char(n int) DataType {
	return DataType{Postgres: "bpchar", MySQL: "char", Length: n}
}

// Decimal is an exact numeric with the given precision and scale.
func Decimal(precision, scale int) DataType {
	return DataType{Postgres: "numeric", MySQL: "decimal", Precision: precision, Scale: scale}
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
	return DataType{Postgres: postgres, MySQL: mysql}
}
