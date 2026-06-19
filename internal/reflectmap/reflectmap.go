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

// Field describes how a struct field maps to a column, carrying both its index
// path and the write-time options declared after the column name in the
// `sqlkit` tag (`sqlkit:"name,opt,..."`).
type Field struct {
	// Path is the field index path, for reflect.Value.FieldByIndex.
	Path []int
	// ReadOnly marks a column scanned on read but never written by INSERT or
	// UPDATE — the `readonly` option, for database-generated columns
	// (identity keys, computed columns, trigger-maintained timestamps).
	ReadOnly bool
	// JSON marks a column whose value is the JSON encoding of the field — the
	// `json` (or `jsonb`) option, so a struct/map/slice field round-trips
	// through a JSON/JSONB column.
	JSON bool
	// Version marks the optimistic-locking version column — the `version`
	// option; SetStruct bumps it and guards the UPDATE with its current value.
	Version bool
	// Touch marks an app-maintained timestamp column — the `touch` option; the
	// field is set to CURRENT_TIMESTAMP on every INSERT and implicit SetStruct
	// UPDATE rather than copied from the struct.
	Touch bool
}

// Fields maps lowercased column names to the field that maps to each. Fields
// register under their `sqlkit` tag name when present, otherwise under both the
// lowercased and snake_case forms of the field name. Embedded structs are
// flattened (except types that scan as a single column, such as time.Time and
// sql.Scanner implementations); shallower fields win conflicts.
func Fields(t reflect.Type) map[string]Field {
	out := map[string]Field{}
	collectFields(t, nil, out)
	return out
}

// FieldsByColumn is the path-only view of Fields, kept for callers that need
// only a field's location and not its tag options.
func FieldsByColumn(t reflect.Type) map[string][]int {
	fields := Fields(t)
	out := make(map[string][]int, len(fields))
	for name, f := range fields {
		out[name] = f.Path
	}
	return out
}

func collectFields(t reflect.Type, prefix []int, out map[string]Field) {
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		path := append(append([]int(nil), prefix...), i)
		name, opts, skip := parseTag(field)
		if skip {
			continue
		}
		// Embedded structs are flattened; exported fields promoted through
		// an unexported embedded type remain settable.
		if name == "" && field.Anonymous && MapsAsStruct(field.Type) {
			collectFields(field.Type, path, out)
			continue
		}
		if field.PkgPath != "" {
			continue
		}
		f := Field{Path: path, ReadOnly: opts.readOnly, JSON: opts.json, Version: opts.version, Touch: opts.touch}
		if name != "" {
			register(out, strings.ToLower(name), f)
			continue
		}
		register(out, strings.ToLower(field.Name), f)
		register(out, SnakeCase(field.Name), f)
	}
}

func register(out map[string]Field, key string, f Field) {
	if existing, ok := out[key]; ok && len(existing.Path) <= len(f.Path) {
		return
	}
	out[key] = f
}

// tagOptions holds the comma-separated options that follow the column name in a
// `sqlkit` tag.
type tagOptions struct {
	readOnly bool
	json     bool
	version  bool
	touch    bool
}

// parseTag reads a field's `sqlkit` tag into a column name and its write
// options. skip is true for a field excluded from mapping (`sqlkit:"-"`); an
// empty name means the column name is derived from the field name instead.
func parseTag(field reflect.StructField) (name string, opts tagOptions, skip bool) {
	tag, ok := field.Tag.Lookup("sqlkit")
	if !ok {
		return "", tagOptions{}, false
	}
	parts := strings.Split(tag, ",")
	name = parts[0]
	if name == "-" {
		if len(parts) == 1 {
			return "", tagOptions{}, true
		}
		name = "" // "-,opt" is not a skip; fall back to the field name
	}
	for _, opt := range parts[1:] {
		switch strings.TrimSpace(opt) {
		case "readonly":
			opts.readOnly = true
		case "json", "jsonb":
			opts.json = true
		case "version":
			opts.version = true
		case "touch":
			opts.touch = true
		}
	}
	return name, opts, false
}
