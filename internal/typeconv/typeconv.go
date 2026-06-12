// Package typeconv maps PostgreSQL column types to Go types.
package typeconv

import (
	"fmt"
	"strings"

	"github.com/sqlkit-dev/sqlkit/internal/genmodel"
)

// GoType describes a generated Go type.
type GoType struct {
	ImportPath string
	Name       string
	Pointer    bool
}

// String renders the type as it appears in Go source.
func (t GoType) String() string {
	if t.Pointer {
		return "*" + t.Name
	}
	return t.Name
}

// pgTypes maps PostgreSQL udt_name values to Go types.
var pgTypes = map[string]GoType{
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

// Convert maps a database column to the Go type its struct field uses.
// Nullable columns map to pointer types, except slices which are already
// nullable. The mapping table is PostgreSQL's; a MySQL table will join it
// when MySQL schema loading lands.
func Convert(column genmodel.Column) (GoType, error) {
	dataType := strings.ToLower(column.DataType)
	// Enum and other user-defined types arrive as their type name; treat
	// unknown non-array types as strings only when explicitly mapped, and
	// fail otherwise so the user notices.
	goType, ok := pgTypes[dataType]
	if !ok {
		return GoType{}, fmt.Errorf("typeconv: unsupported PostgreSQL type %q (column %q)", column.DataType, column.Name)
	}
	if column.Nullable && !strings.HasPrefix(goType.Name, "[]") {
		goType.Pointer = true
	}
	return goType, nil
}
