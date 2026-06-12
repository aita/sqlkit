package codegen

import (
	"bytes"
	"fmt"
	"go/format"
	"sort"
	"strings"
	"text/template"

	"github.com/aita/sqlkit/schema"
)

// File is a single generated source file: a suggested name and its formatted
// contents.
type File struct {
	Name    string
	Content []byte
}

// Options configures generation. The zero value (with PackageName set) emits a
// single file of table bindings.
type Options struct {
	// PackageName is the package of the generated files. Required.
	PackageName string

	// Tables, when non-empty, restricts generation to these table names.
	Tables []string

	// Dialect overrides Schema.Dialect ("postgres" or "mysql"); it selects the
	// default SQL→Go type table.
	Dialect string

	// FilePerTable writes one file per table (named after the table) instead of
	// a single combined file.
	FilePerTable bool

	// FileName overrides the single-file output name (default "tables.go").
	// Ignored when FilePerTable is set.
	FileName string

	// EmitDTO additionally generates a row struct (DTO) per table.
	EmitDTO bool

	// DTOSeparateFile writes the DTOs into their own file(s), separate from the
	// table bindings.
	DTOSeparateFile bool

	// TypeMapper customizes the SQL→Go type mapping. Defaults to
	// DefaultTypeMapper{}.
	TypeMapper TypeMapper

	// DTOName derives the DTO type name from a table. Defaults to a singular,
	// collision-avoiding name (e.g. table "users" -> "User"). When the derived
	// name collides with a binding identifier, "Row" is appended.
	DTOName func(table schema.Table) string
}

const sqlkitImport = "github.com/aita/sqlkit"

type columnModel struct {
	Field      string
	Name       string
	GoType     string
	GoTypeImp  string
	PrimaryKey bool
	HasDefault bool
}

type tableModel struct {
	TypeName  string
	VarName   string
	DTOName   string
	Schema    string
	Name      string
	Qualified string
	Columns   []columnModel
}

// Generate renders the schema into one or more Go source files according to
// opts and returns their formatted contents.
func Generate(model schema.Schema, opts Options) ([]File, error) {
	if opts.PackageName == "" {
		return nil, fmt.Errorf("codegen: package name is required")
	}
	dialect := opts.Dialect
	if dialect == "" {
		dialect = model.Dialect
	}
	mapper := opts.TypeMapper
	if mapper == nil {
		mapper = DefaultTypeMapper{}
	}
	dtoName := opts.DTOName
	if dtoName == nil {
		dtoName = defaultDTOName
	}

	tables := filterTables(model.Tables, opts.Tables)
	if len(tables) == 0 {
		return nil, fmt.Errorf("codegen: no tables to generate")
	}

	// Reserve binding identifiers so generated DTO names never collide.
	reserved := map[string]bool{}
	for _, table := range tables {
		varName := goName(table.Name)
		reserved[varName] = true
		reserved[varName+"Table"] = true
	}

	models := make([]*tableModel, 0, len(tables))
	for _, table := range tables {
		tm, err := buildTable(table, dialect, mapper)
		if err != nil {
			return nil, err
		}
		if opts.EmitDTO {
			name := dtoName(table)
			for reserved[name] {
				name += "Row"
			}
			reserved[name] = true
			tm.DTOName = name
		}
		models = append(models, tm)
	}

	return assemble(models, opts)
}

// buildTable builds the binding model for a single table.
func buildTable(table schema.Table, dialect string, mapper TypeMapper) (*tableModel, error) {
	varName := goName(table.Name)
	tm := &tableModel{
		TypeName:  varName + "Table",
		VarName:   varName,
		Schema:    table.Schema,
		Name:      table.Name,
		Qualified: qualified(table.Schema, table.Name),
	}
	for _, column := range table.Columns {
		goType, err := mapper.GoTypeFor(column, dialect)
		if err != nil {
			return nil, fmt.Errorf("codegen: table %s: %w", table.Name, err)
		}
		field := fieldName(column.Name)
		cm := columnModel{
			Field:      field,
			Name:       column.Name,
			GoType:     goType.String(),
			GoTypeImp:  goType.ImportPath,
			PrimaryKey: column.PrimaryKey,
			HasDefault: column.HasDefault,
		}
		tm.Columns = append(tm.Columns, cm)
	}
	return tm, nil
}

func defaultDTOName(table schema.Table) string {
	return goName(singular(table.Name))
}

func filterTables(tables []schema.Table, only []string) []schema.Table {
	if len(only) == 0 {
		return tables
	}
	want := map[string]bool{}
	for _, name := range only {
		want[name] = true
	}
	var out []schema.Table
	for _, table := range tables {
		if want[table.Name] {
			out = append(out, table)
		}
	}
	return out
}

func qualified(schemaName, name string) string {
	if schemaName == "" {
		return name
	}
	return schemaName + "." + name
}

// assemble groups the rendered table segments into files per the layout
// options.
func assemble(models []*tableModel, opts Options) ([]File, error) {
	var files []File
	emit := func(fb *fileBuf, name string) error {
		file, err := fb.file(opts.PackageName, name)
		if err != nil {
			return err
		}
		files = append(files, file)
		return nil
	}

	if opts.FilePerTable {
		for _, tm := range models {
			fb := newFileBuf()
			fb.add(bindingImports(tm)...)
			fb.write(renderBinding(tm))
			if opts.EmitDTO && !opts.DTOSeparateFile {
				fb.add(dtoImports(tm)...)
				fb.write(renderDTO(tm))
			}
			if err := emit(fb, tm.Name+".go"); err != nil {
				return nil, err
			}

			if opts.EmitDTO && opts.DTOSeparateFile {
				db := newFileBuf()
				db.add(dtoImports(tm)...)
				db.write(renderDTO(tm))
				if err := emit(db, tm.Name+"_dto.go"); err != nil {
					return nil, err
				}
			}
		}
		return files, nil
	}

	fb := newFileBuf()
	for _, tm := range models {
		fb.add(bindingImports(tm)...)
		fb.write(renderBinding(tm))
		if opts.EmitDTO && !opts.DTOSeparateFile {
			fb.add(dtoImports(tm)...)
			fb.write(renderDTO(tm))
		}
	}
	name := opts.FileName
	if name == "" {
		name = "tables.go"
	}
	if err := emit(fb, name); err != nil {
		return nil, err
	}

	if opts.EmitDTO && opts.DTOSeparateFile {
		db := newFileBuf()
		for _, tm := range models {
			db.add(dtoImports(tm)...)
			db.write(renderDTO(tm))
		}
		if err := emit(db, "dtos.go"); err != nil {
			return nil, err
		}
	}
	return files, nil
}

func bindingImports(tm *tableModel) []string {
	imports := []string{sqlkitImport}
	for _, c := range tm.Columns {
		if c.GoTypeImp != "" {
			imports = append(imports, c.GoTypeImp)
		}
	}
	return imports
}

func dtoImports(tm *tableModel) []string {
	var imports []string
	for _, c := range tm.Columns {
		if c.GoTypeImp != "" {
			imports = append(imports, c.GoTypeImp)
		}
	}
	return imports
}

// fileBuf accumulates a file's imports and body.
type fileBuf struct {
	imports map[string]bool
	body    strings.Builder
}

func newFileBuf() *fileBuf { return &fileBuf{imports: map[string]bool{}} }

func (b *fileBuf) add(paths ...string) {
	for _, p := range paths {
		if p != "" {
			b.imports[p] = true
		}
	}
}

func (b *fileBuf) write(segment string) {
	if segment == "" {
		return
	}
	if b.body.Len() > 0 {
		b.body.WriteString("\n")
	}
	b.body.WriteString(segment)
}

func (b *fileBuf) file(pkg, name string) (File, error) {
	return b.fileWithHeader(pkg, name, "// Code generated by sqlkitgen. DO NOT EDIT.\n\n")
}

func (b *fileBuf) fileWithHeader(pkg, name, header string) (File, error) {
	var buf bytes.Buffer
	buf.WriteString(header)
	buf.WriteString("package " + pkg + "\n")
	if len(b.imports) > 0 {
		paths := make([]string, 0, len(b.imports))
		for p := range b.imports {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		buf.WriteString("\nimport (\n")
		for _, p := range paths {
			buf.WriteString("\t\"" + p + "\"\n")
		}
		buf.WriteString(")\n")
	}
	buf.WriteString("\n")
	buf.WriteString(b.body.String())

	source, err := format.Source(buf.Bytes())
	if err != nil {
		return File{}, fmt.Errorf("codegen: format generated file %s: %w\n%s", name, err, buf.String())
	}
	return File{Name: name, Content: source}, nil
}

func renderBinding(tm *tableModel) string {
	return execute(bindingTemplate, tm)
}

func renderDTO(tm *tableModel) string {
	return execute(dtoTemplate, tm)
}

func execute(tmpl *template.Template, tm *tableModel) string {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, tm); err != nil {
		panic(fmt.Sprintf("codegen: render %s: %v", tmpl.Name(), err))
	}
	return buf.String()
}

var bindingTemplate = template.Must(template.New("binding").Parse(`// {{.TypeName}} is the typed binding for table {{.Qualified}}.
type {{.TypeName}} struct {
	info sqlkit.TableInfo
{{- range .Columns}}
	{{.Field}} sqlkit.Column[{{.GoType}}]
{{- end}}
}

// {{.VarName}} provides typed access to table {{.Qualified}}.
var {{.VarName}} = func() {{.TypeName}} {
	info := sqlkit.TableInfo{Schema: {{printf "%q" .Schema}}, Name: {{printf "%q" .Name}}}
	return {{.TypeName}}{
		info: info,
{{- range .Columns}}
		{{.Field}}: sqlkit.NewColumn[{{.GoType}}](info, {{printf "%q" .Name}}),
{{- end}}
	}
}()

// Info returns the table identity.
func (t {{.TypeName}}) Info() sqlkit.TableInfo { return t.info }

// SQLSource marks the table as a FROM/JOIN source.
func (t {{.TypeName}}) SQLSource() sqlkit.SourceExpression { return t.info.SQLSource() }

// Columns returns all column bindings in declaration order.
func (t {{.TypeName}}) Columns() []sqlkit.AnyColumn {
	return []sqlkit.AnyColumn{ {{- range $i, $c := .Columns}}{{if $i}}, {{end}}t.{{$c.Field}}{{end -}} }
}

// PrimaryKey returns the primary key column bindings.
func (t {{.TypeName}}) PrimaryKey() []sqlkit.AnyColumn {
	return []sqlkit.AnyColumn{ {{- $first := true}}{{range .Columns}}{{if .PrimaryKey}}{{if not $first}}, {{end}}{{$first = false}}t.{{.Field}}{{end}}{{end -}} }
}

// Defaulted returns the columns with database-side defaults.
func (t {{.TypeName}}) Defaulted() []sqlkit.AnyColumn {
	return []sqlkit.AnyColumn{ {{- $first := true}}{{range .Columns}}{{if .HasDefault}}{{if not $first}}, {{end}}{{$first = false}}t.{{.Field}}{{end}}{{end -}} }
}

var _ sqlkit.TableMeta = {{.VarName}}
`))

var dtoTemplate = template.Must(template.New("dto").Parse("// {{.DTOName}} is a generated row struct for table {{.Qualified}}; any struct\n" +
	"// following Scan's mapping rules works as a destination or Insert value.\n" +
	"type {{.DTOName}} struct {\n" +
	"{{- range .Columns}}\n" +
	"\t{{.Field}} {{.GoType}} `sqlkit:\"{{.Name}}\"`\n" +
	"{{- end}}\n" +
	"}\n"))
