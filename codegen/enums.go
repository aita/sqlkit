package codegen

import (
	"fmt"
	"strings"

	"github.com/aita/sqlkit/meta"
)

// renderEnum generates a Go string type and a const block of its labels for one
// enum: type Mood string; const ( MoodHappy Mood = "happy"; ... ).
func renderEnum(e meta.Enum) string {
	typeName := goName(e.Name)
	var b strings.Builder
	fmt.Fprintf(&b, "// %s is the %q enum type.\n", typeName, e.QualifiedName())
	fmt.Fprintf(&b, "type %s string\n\n", typeName)
	fmt.Fprintf(&b, "// Values of the %s enum type.\n", typeName)
	b.WriteString("const (\n")
	for _, v := range e.Values {
		fmt.Fprintf(&b, "\t%s%s %s = %q\n", typeName, goName(v), typeName, v)
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
