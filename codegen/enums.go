package codegen

import (
	"fmt"
	"strings"

	"github.com/aita/sqlkit/meta"
)

// columnGoType resolves a column's Go type: a column whose DB type names a
// catalog enum (decl.EnumType) maps to the generated Go enum type — applying
// nullability like the mapper does — and every other column defers to the
// TypeMapper.
func columnGoType(table meta.Table, column meta.Column, dialect string, mapper TypeMapper, enumGo map[string]meta.GoType) (meta.GoType, error) {
	if len(enumGo) > 0 && column.Type != nil {
		if engine, ok := column.Type.Engine(dialect); ok {
			if gt, ok := enumGo[engine.Name]; ok {
				return applyNullable(gt, column), nil
			}
		}
	}
	return mapper.GoTypeFor(table, column, dialect)
}

// enumGoTypes maps the SQL type names an enum column can carry (the enum's bare
// name and its schema-qualified name) to the generated Go enum type, so a column
// declared with decl.EnumType resolves to that type without a manual override.
func enumGoTypes(enums []meta.Enum) map[string]meta.GoType {
	if len(enums) == 0 {
		return nil
	}
	m := make(map[string]meta.GoType, len(enums)*2)
	for _, e := range enums {
		gt := meta.GoType{Name: goName(e.Name)}
		m[e.Name] = gt
		m[e.QualifiedName()] = gt
	}
	return m
}

// renderEnum generates a Go string type and a const block of its labels for one
// enum: type Mood string; const ( MoodHappy Mood = "happy"; ... ).
func renderEnum(e meta.Enum) string {
	typeName := goName(e.Name)
	var b strings.Builder
	fmt.Fprintf(&b, "// %s is the %q enum type.\n", typeName, e.QualifiedName())
	fmt.Fprintf(&b, "type %s string\n\n", typeName)
	fmt.Fprintf(&b, "// Values of the %s enum type.\n", typeName)
	b.WriteString("const (\n")
	// Distinct labels can map to the same Go name (e.g. "foo", "foo_" and "foo__"
	// all yield "Foo"), and a catalog could even carry a duplicate label; dedupe
	// the const names so the block never redeclares an identifier. The type name
	// is reserved too, so a label can't collide with the enum type itself.
	used := map[string]bool{typeName: true}
	for _, v := range e.Values {
		name := uniqueName(used, typeName+goName(v))
		fmt.Fprintf(&b, "\t%s %s = %q\n", name, typeName, v)
	}
	b.WriteString(")\n")
	return b.String()
}

// enumFile assembles the generated enums into a single "enums.go" file, or
// reports ok=false when there are no enums.
func enumFile(enums []meta.Enum, opts Options) (File, bool, error) {
	if len(enums) == 0 {
		return File{}, false, nil
	}
	fb := newFileBuf()
	for _, e := range enums {
		fb.write(renderEnum(e))
	}
	file, err := fb.file(opts.PackageName, "enums.go")
	if err != nil {
		return File{}, false, err
	}
	return file, true, nil
}
