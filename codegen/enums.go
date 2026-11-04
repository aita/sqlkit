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
				// Wrap the element type in a slice for an array-of-enum column (mood[])
				// before applying nullability, exactly as the non-enum mapper path
				// (resolvedType) does — otherwise an array column resolves to the bare
				// element type (Mood instead of []Mood), a silent shape mismatch that
				// only surfaces as a scan/bind failure at runtime against a real database.
				return resolvedType(gt, engine, column), nil
			}
		}
	}
	return mapper.GoTypeFor(table, column, dialect)
}

// enumModel is one enum with its final, collision-free Go identifiers: the type
// name and the per-label const names, all deduped in Generate against the shared
// package-wide reserved set (binding vars, DTOs, other enums and their labels).
type enumModel struct {
	Enum     meta.Enum
	TypeName string
	Labels   []string // one Go const name per Enum.Values entry
}

// renderEnum generates a Go string type and a const block of its labels for one
// enum: type Mood string; const ( MoodHappy Mood = "happy"; ... ). The
// identifiers are precomputed (and deduped) in em.
func renderEnum(em enumModel) string {
	typeName := em.TypeName
	var b strings.Builder
	fmt.Fprintf(&b, "// %s is the %q enum type.\n", typeName, em.Enum.QualifiedName())
	fmt.Fprintf(&b, "type %s string\n\n", typeName)
	fmt.Fprintf(&b, "// Values of the %s enum type.\n", typeName)
	b.WriteString("const (\n")
	for i, v := range em.Enum.Values {
		fmt.Fprintf(&b, "\t%s %s = %q\n", em.Labels[i], typeName, v)
	}
	b.WriteString(")\n")
	return b.String()
}

// enumFile assembles the generated enums into a single "enums.go" file, or
// reports ok=false when there are no enums.
func enumFile(models []enumModel, opts Options) (File, bool, error) {
	if len(models) == 0 {
		return File{}, false, nil
	}
	fb := newFileBuf()
	for _, em := range models {
		fb.write(renderEnum(em))
	}
	file, err := fb.file(opts.PackageName, "enums.go")
	if err != nil {
		return File{}, false, err
	}
	return file, true, nil
}
