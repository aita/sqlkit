package sqldef

// DataType is a column type in a design. It carries the engine type name for
// each supported dialect plus optional length/precision modifiers; Build
// resolves it to the target dialect's name.
type DataType struct {
	postgres  string
	mysql     string
	length    int
	precision int
	scale     int
}

// engineName returns the engine type name for the dialect and whether the
// type is available on it.
func (t DataType) engineName(dialect string) (string, bool) {
	switch dialect {
	case "mysql":
		return t.mysql, t.mysql != ""
	default:
		return t.postgres, t.postgres != ""
	}
}

// Portable column types. Each resolves to the matching engine type of the
// dialect the design is built for.
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
// dialect. An empty name makes the type unavailable on that dialect, which
// Build reports as an error.
func TypeFor(postgres, mysql string) DataType {
	return DataType{postgres: postgres, mysql: mysql}
}
