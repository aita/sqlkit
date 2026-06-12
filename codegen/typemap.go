package codegen

import (
	"fmt"
	"strings"

	"github.com/aita/sqlkit/schema"
)

// GoType is an alias for schema.GoType, re-exported so library users that only
// import codegen can spell type overrides as codegen.GoType.
type GoType = schema.GoType

// TypeMapper resolves the Go type a column's field uses. Implement it (or use
// DefaultTypeMapper with Overrides) to customize the SQL→Go mapping when using
// codegen as a library.
type TypeMapper interface {
	GoTypeFor(column schema.Column, dialect string) (schema.GoType, error)
}

// TypeMapperFunc adapts a plain function to the TypeMapper interface.
type TypeMapperFunc func(column schema.Column, dialect string) (schema.GoType, error)

// GoTypeFor calls f.
func (f TypeMapperFunc) GoTypeFor(column schema.Column, dialect string) (schema.GoType, error) {
	return f(column, dialect)
}

// DefaultTypeMapper maps standard PostgreSQL and MySQL types to Go types and
// turns nullable columns into pointer types (slices, already nullable, are left
// as-is). Overrides, keyed by the lowercase SQL type name, take precedence over
// the built-in tables — e.g. to map "numeric" to a decimal type:
//
//	codegen.DefaultTypeMapper{Overrides: map[string]codegen.GoType{
//		"numeric": {ImportPath: "github.com/shopspring/decimal", Name: "decimal.Decimal"},
//	}}
type DefaultTypeMapper struct {
	Overrides map[string]schema.GoType
}

// GoTypeFor implements TypeMapper.
func (m DefaultTypeMapper) GoTypeFor(column schema.Column, dialect string) (schema.GoType, error) {
	dataType := strings.ToLower(strings.TrimSpace(column.DataType))
	if gt, ok := m.Overrides[dataType]; ok {
		return applyNullable(gt, column), nil
	}
	table := pgTypes
	label := "PostgreSQL"
	if dialect == "mysql" {
		table = mysqlTypes
		label = "MySQL"
	}
	gt, ok := table[dataType]
	if !ok {
		return schema.GoType{}, fmt.Errorf("codegen: unsupported %s type %q (column %q); add a type override via Options.TypeMapper", label, column.DataType, column.Name)
	}
	return applyNullable(gt, column), nil
}

func applyNullable(gt schema.GoType, column schema.Column) schema.GoType {
	if column.Nullable && !strings.HasPrefix(gt.Name, "[]") {
		gt.Pointer = true
	}
	return gt
}

// pgTypes maps PostgreSQL udt_name values to Go types.
var pgTypes = map[string]schema.GoType{
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
var mysqlTypes = map[string]schema.GoType{
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
