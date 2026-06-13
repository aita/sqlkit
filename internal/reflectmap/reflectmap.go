// Package reflectmap maps struct fields to database columns, the single
// definition of the name-mapping rules (sqlkit tag, exact name, snake_case,
// flattened embeds) shared by result scanning, INSERT value extraction, and the
// ORM preloader.
package reflectmap

import (
	stdsql "database/sql"
	"reflect"
	"strings"
	"time"

	"github.com/aita/sqlkit/internal/names"
)

var (
	scannerType = reflect.TypeOf((*stdsql.Scanner)(nil)).Elem()
	timeType    = reflect.TypeOf(time.Time{})
)

// MapsAsStruct reports whether values of t are filled field-by-field rather
// than scanned as a single column.
func MapsAsStruct(t reflect.Type) bool {
	if t.Kind() != reflect.Struct {
		return false
	}
	if t == timeType {
		return false
	}
	if t.Implements(scannerType) || reflect.PointerTo(t).Implements(scannerType) {
		return false
	}
	return true
}

// FieldsByColumn maps lowercased column names to field index paths. Fields
// register under their `sqlkit` tag when present, otherwise under both the
// lowercased and snake_case forms of the field name. Embedded structs are
// flattened (except types that scan as a single column, such as time.Time and
// sql.Scanner implementations); shallower fields win conflicts.
func FieldsByColumn(t reflect.Type) map[string][]int {
	out := map[string][]int{}
	collectFields(t, nil, out)
	return out
}

func collectFields(t reflect.Type, prefix []int, out map[string][]int) {
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		path := append(append([]int(nil), prefix...), i)
		tag := FieldTag(field)
		if tag == "-" {
			continue
		}
		// Embedded structs are flattened; exported fields promoted through
		// an unexported embedded type remain settable.
		if tag == "" && field.Anonymous && MapsAsStruct(field.Type) {
			collectFields(field.Type, path, out)
			continue
		}
		if field.PkgPath != "" {
			continue
		}
		if tag != "" {
			register(out, strings.ToLower(tag), path)
			continue
		}
		register(out, strings.ToLower(field.Name), path)
		register(out, names.SnakeCase(field.Name), path)
	}
}

func register(out map[string][]int, key string, path []int) {
	if existing, ok := out[key]; ok && len(existing) <= len(path) {
		return
	}
	out[key] = path
}

// FieldTag returns the column name from a field's `sqlkit` tag, or "" when the
// tag is absent or empty (the literal "-" is returned, signaling "skip").
func FieldTag(field reflect.StructField) string {
	if tag, ok := field.Tag.Lookup("sqlkit"); ok {
		name := strings.Split(tag, ",")[0]
		if name != "" {
			return name
		}
	}
	return ""
}
