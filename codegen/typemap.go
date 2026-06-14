package codegen

import (
	"fmt"
	"strings"

	"github.com/aita/sqlkit/meta"
)

// GoType is an alias for meta.GoType, re-exported so library users that only
// import codegen can spell type overrides as codegen.GoType.
type GoType = meta.GoType

// TypeMapper resolves the Go type a column's field uses, for a given dialect.
// The table is supplied so a mapper can key its decision on the table and column
// names, not just the SQL type. Implement it (or build a DefaultTypeMapper with
// NewDefaultTypeMapper) to customize the SQL→Go mapping when using codegen as a
// library.
type TypeMapper interface {
	GoTypeFor(table meta.Table, column meta.Column, dialect string) (meta.GoType, error)
}

// TypeMapperFunc adapts a plain function to the TypeMapper interface.
type TypeMapperFunc func(table meta.Table, column meta.Column, dialect string) (meta.GoType, error)

// GoTypeFor calls f.
func (f TypeMapperFunc) GoTypeFor(table meta.Table, column meta.Column, dialect string) (meta.GoType, error) {
	return f(table, column, dialect)
}

// TypeMapperConfig initializes a DefaultTypeMapper; pass it to
// NewDefaultTypeMapper. All fields are optional — the zero config maps standard
// types to Go types out of the box.
type TypeMapperConfig struct {
	// Overrides maps a lowercase SQL/engine type name to a Go type, e.g. to map
	// every "numeric" column to a decimal type:
	//
	//	Overrides: map[string]codegen.GoType{
	//		"numeric": {ImportPath: "github.com/shopspring/decimal", Name: "decimal.Decimal"},
	//	}
	Overrides map[string]meta.GoType
	// ColumnOverrides maps a specific column to a Go type, keyed by
	// "schema.table.column", "table.column", or bare "column" (most specific
	// match wins), regardless of the column's SQL type:
	//
	//	ColumnOverrides: map[string]codegen.GoType{
	//		"users.id": {ImportPath: "github.com/google/uuid", Name: "uuid.UUID"},
	//		"metadata": {ImportPath: "encoding/json", Name: "json.RawMessage"},
	//	}
	ColumnOverrides map[string]meta.GoType
	// OverrideFunc, when set, is consulted first for each column. Return ok=true
	// with the resolved Go type to use it, ok=false to fall through to the maps,
	// pinned types, and built-in tables, or a non-nil error to fail generation.
	// It receives the table so it can decide on the table and column names.
	OverrideFunc func(table meta.Table, column meta.Column, dialect string) (gt meta.GoType, ok bool, err error)
}

// DefaultTypeMapper maps standard PostgreSQL, MySQL, and SQLite types to Go
// types and turns nullable columns into pointer types (slices, already nullable,
// are left as-is). Construct it with NewDefaultTypeMapper; the zero value is the
// default mapper with no customization. It resolves a column's Go type in
// precedence order:
//
//  1. Config.OverrideFunc, if set and it returns ok.
//  2. Config.ColumnOverrides, by column name (most specific key wins).
//  3. Config.Overrides, by lowercase SQL type name.
//  4. A Go type pinned on the column's meta.Type at the schema source (the
//     sqldef DSL's DataType.Go), so a custom DB type carries its Go type along.
//  5. The built-in PostgreSQL/MySQL/SQLite type tables.
//
// Nullability is applied to the resolved type at every level (a nullable column
// becomes a pointer), so overrides pin the bare type.
type DefaultTypeMapper struct {
	cfg TypeMapperConfig
}

// NewDefaultTypeMapper builds a DefaultTypeMapper from the config.
func NewDefaultTypeMapper(cfg TypeMapperConfig) DefaultTypeMapper {
	return DefaultTypeMapper{cfg: cfg}
}

// GoTypeFor implements TypeMapper.
func (m DefaultTypeMapper) GoTypeFor(table meta.Table, column meta.Column, dialect string) (meta.GoType, error) {
	if m.cfg.OverrideFunc != nil {
		gt, ok, err := m.cfg.OverrideFunc(table, column, dialect)
		if err != nil {
			return meta.GoType{}, err
		}
		if ok {
			return applyNullable(gt, column), nil
		}
	}
	if gt, ok := m.columnOverride(table, column); ok {
		return applyNullable(gt, column), nil
	}
	if column.Type == nil {
		return meta.GoType{}, fmt.Errorf("codegen: column %q has no type", column.Name)
	}
	engine, ok := column.Type.Engine(dialect)
	if !ok {
		return meta.GoType{}, fmt.Errorf("codegen: column %q has no type on dialect %s", column.Name, dialect)
	}
	dataType := strings.ToLower(strings.TrimSpace(engine.Name))
	if gt, ok := m.cfg.Overrides[dataType]; ok {
		return resolvedType(gt, engine, column), nil
	}
	if typed, ok := column.Type.(meta.GoTyped); ok {
		if gt, ok := typed.GoType(dialect); ok {
			return resolvedType(gt, engine, column), nil
		}
	}
	stdTable, label := tableForDialect(dialect)
	gt, ok := stdTable[dataType]
	if !ok {
		return meta.GoType{}, fmt.Errorf("codegen: unsupported %s type %q (column %q); add a type override via NewDefaultTypeMapper or pin one with sqldef's DataType.Go", label, engine.Name, column.Name)
	}
	return resolvedType(gt, engine, column), nil
}

// resolvedType finishes a Go type resolved from the column's SQL type (the
// Overrides map, a pinned meta.GoTyped, or the built-in tables): it wraps an
// array element in a Go slice and applies nullability. Column-level overrides
// (OverrideFunc, ColumnOverrides) bypass it — they spell the full Go type.
func resolvedType(gt meta.GoType, engine meta.Engine, column meta.Column) meta.GoType {
	if engine.Array {
		gt.Name = "[]" + gt.Name
	}
	return applyNullable(gt, column)
}

// columnOverride looks up a name-based override for the column, trying the
// fully qualified name first and falling back to less specific keys.
func (m DefaultTypeMapper) columnOverride(table meta.Table, column meta.Column) (meta.GoType, bool) {
	if len(m.cfg.ColumnOverrides) == 0 {
		return meta.GoType{}, false
	}
	if table.Schema.Name != "" {
		if gt, ok := m.cfg.ColumnOverrides[table.Schema.Name+"."+table.Name+"."+column.Name]; ok {
			return gt, true
		}
	}
	if table.Name != "" {
		if gt, ok := m.cfg.ColumnOverrides[table.Name+"."+column.Name]; ok {
			return gt, true
		}
	}
	gt, ok := m.cfg.ColumnOverrides[column.Name]
	return gt, ok
}

func applyNullable(gt meta.GoType, column meta.Column) meta.GoType {
	if column.Nullable && !strings.HasPrefix(gt.Name, "[]") {
		gt.Pointer = true
	}
	return gt
}

// tableForDialect returns the built-in SQL→Go type table for the dialect and a
// human label for error messages, defaulting to PostgreSQL.
func tableForDialect(dialect string) (map[string]meta.GoType, string) {
	switch dialect {
	case "mysql":
		return mysqlTypes, "MySQL"
	case "sqlite":
		return sqliteTypes, "SQLite"
	default:
		return pgTypes, "PostgreSQL"
	}
}

// pgTypes maps PostgreSQL udt_name values to Go types.
var pgTypes = map[string]meta.GoType{
	"bool":        {Name: "bool"},
	"int2":        {Name: "int16"},
	"int4":        {Name: "int32"},
	"int8":        {Name: "int64"},
	"float4":      {Name: "float32"},
	"float8":      {Name: "float64"},
	"numeric":     {Name: "string"},
	"money":       {Name: "string"},
	"text":        {Name: "string"},
	"varchar":     {Name: "string"},
	"bpchar":      {Name: "string"},
	"name":        {Name: "string"},
	"uuid":        {Name: "string"},
	"citext":      {Name: "string"},
	"bytea":       {Name: "[]byte"},
	"json":        {Name: "[]byte"},
	"jsonb":       {Name: "[]byte"},
	"xml":         {Name: "string"},
	"inet":        {Name: "string"},
	"cidr":        {Name: "string"},
	"macaddr":     {Name: "string"},
	"interval":    {Name: "string"},
	"date":        {ImportPath: "time", Name: "time.Time"},
	"time":        {ImportPath: "time", Name: "time.Time"},
	"timetz":      {ImportPath: "time", Name: "time.Time"},
	"timestamp":   {ImportPath: "time", Name: "time.Time"},
	"timestamptz": {ImportPath: "time", Name: "time.Time"},
}

// mysqlTypes maps MySQL information_schema data_type values to Go types.
var mysqlTypes = map[string]meta.GoType{
	"bool":       {Name: "bool"},
	"boolean":    {Name: "bool"},
	"tinyint":    {Name: "int8"},
	"smallint":   {Name: "int16"},
	"mediumint":  {Name: "int32"},
	"int":        {Name: "int32"},
	"integer":    {Name: "int32"},
	"bigint":     {Name: "int64"},
	"year":       {Name: "int16"},
	"float":      {Name: "float32"},
	"double":     {Name: "float64"},
	"real":       {Name: "float64"},
	"decimal":    {Name: "string"},
	"numeric":    {Name: "string"},
	"bit":        {Name: "[]byte"},
	"char":       {Name: "string"},
	"varchar":    {Name: "string"},
	"tinytext":   {Name: "string"},
	"text":       {Name: "string"},
	"mediumtext": {Name: "string"},
	"longtext":   {Name: "string"},
	"enum":       {Name: "string"},
	"set":        {Name: "string"},
	"json":       {Name: "[]byte"},
	"binary":     {Name: "[]byte"},
	"varbinary":  {Name: "[]byte"},
	"tinyblob":   {Name: "[]byte"},
	"blob":       {Name: "[]byte"},
	"mediumblob": {Name: "[]byte"},
	"longblob":   {Name: "[]byte"},
	"date":       {ImportPath: "time", Name: "time.Time"},
	"datetime":   {ImportPath: "time", Name: "time.Time"},
	"timestamp":  {ImportPath: "time", Name: "time.Time"},
	"time":       {ImportPath: "time", Name: "time.Time"},
}

// sqliteTypes maps the engine type names the sqldef registry emits for SQLite
// (plus the common storage-class spellings) to Go types. SQLite's type affinity
// is loose, so the mapping keys on the declared type name.
var sqliteTypes = map[string]meta.GoType{
	"integer":  {Name: "int64"},
	"int":      {Name: "int64"},
	"bigint":   {Name: "int64"},
	"real":     {Name: "float64"},
	"float":    {Name: "float64"},
	"double":   {Name: "float64"},
	"numeric":  {Name: "string"},
	"decimal":  {Name: "string"},
	"boolean":  {Name: "bool"},
	"text":     {Name: "string"},
	"varchar":  {Name: "string"},
	"char":     {Name: "string"},
	"clob":     {Name: "string"},
	"json":     {Name: "[]byte"},
	"blob":     {Name: "[]byte"},
	"date":     {ImportPath: "time", Name: "time.Time"},
	"datetime": {ImportPath: "time", Name: "time.Time"},
	"time":     {ImportPath: "time", Name: "time.Time"},
}
