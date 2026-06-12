package codegen

import (
	"bytes"
	"fmt"
	"go/format"
	"sort"
	"strings"
	"text/template"

	"github.com/sqlkit-dev/sqlkit/schema"
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

	// OmitRelations suppresses the foreign-key Relations methods. Relations are
	// pure navigation: each returns the related table binding (metadata), never
	// an executor-bound query — the bindings are not an ORM.
	OmitRelations bool

	// TypeMapper customizes the SQL→Go type mapping. Defaults to
	// DefaultTypeMapper{}.
	TypeMapper TypeMapper

	// DTOName derives the DTO type name from a table. Defaults to a singular,
	// collision-avoiding name (e.g. table "users" -> "User"). When the derived
	// name collides with a binding identifier, "Row" is appended.
	DTOName func(table schema.Table) string
}

const sqlkitImport = "github.com/sqlkit-dev/sqlkit/sqlkit"

type columnModel struct {
	Field      string
	Name       string
	GoType     string
	GoTypeImp  string
	Options    []string
	PrimaryKey bool
	HasDefault bool
}

// relationModel is a foreign-key navigation method that returns the related
// table binding. It carries no executor or query — the binding is metadata, not
// an ORM entity.
type relationModel struct {
	Comment    string
	Method     string
	ReturnType string // type name of the related binding, e.g. "UsersTable"
	ReturnVar  string // package var of the related binding, e.g. "Users"
}

type tableModel struct {
	TypeName  string
	VarName   string
	DTOName   string
	Schema    string
	Name      string
	Qualified string
	Columns   []columnModel
	Relations []relationModel
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
	index := map[string]*tableModel{}
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
		index[table.Name] = tm
	}

	if !opts.OmitRelations {
		buildRelations(tables, index)
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
			Options:    columnOptions(column),
			PrimaryKey: column.PrimaryKey,
			HasDefault: column.HasDefault,
		}
		tm.Columns = append(tm.Columns, cm)
	}
	return tm, nil
}

// columnOptions renders the sqlkit.NewColumn functional options for a column.
func columnOptions(column schema.Column) []string {
	var opts []string
	if column.DataType != "" {
		opts = append(opts, fmt.Sprintf("sqlkit.WithSQLType(%q)", column.DataType))
	}
	if column.Length > 0 {
		opts = append(opts, fmt.Sprintf("sqlkit.WithLength(%d)", column.Length))
	}
	if column.Precision > 0 {
		opts = append(opts, fmt.Sprintf("sqlkit.WithPrecision(%d, %d)", column.Precision, column.Scale))
	}
	if column.PrimaryKey {
		opts = append(opts, "sqlkit.WithPrimaryKey()")
	}
	if column.Nullable {
		opts = append(opts, "sqlkit.WithNullable()")
	}
	if column.HasDefault {
		opts = append(opts, "sqlkit.WithDefault()")
	}
	return opts
}

// buildRelations turns foreign keys into navigation methods that return the
// related table binding: a belongs-to method on the referencing table and a
// has-many method on the referenced table. They return metadata, not queries —
// the bindings are not an ORM.
func buildRelations(tables []schema.Table, index map[string]*tableModel) {
	used := map[string]map[string]bool{}
	reserve := func(tm *tableModel) map[string]bool {
		set, ok := used[tm.Name]
		if !ok {
			set = map[string]bool{}
			for name := range reservedFieldNames {
				set[name] = true
			}
			for _, c := range tm.Columns {
				set[c.Field] = true
			}
			used[tm.Name] = set
		}
		return set
	}
	unique := func(set map[string]bool, base string) string {
		name := base
		for i := 2; set[name]; i++ {
			name = fmt.Sprintf("%s%d", base, i)
		}
		set[name] = true
		return name
	}

	for _, child := range tables {
		childModel := index[child.Name]
		for _, fk := range child.ForeignKeys {
			parent, ok := index[fk.RefTable]
			if !ok || len(fk.Columns) == 0 || len(fk.Columns) != len(fk.RefColumns) {
				continue
			}
			parentTable := tableByName(tables, fk.RefTable)

			// belongs-to: child.<Parent>() returns the referenced table binding.
			set := reserve(childModel)
			method := unique(set, belongsToName(fk, parentTable))
			childModel.Relations = append(childModel.Relations, relationModel{
				Comment:    fmt.Sprintf("// %s returns the %s table referenced by %s (foreign key %s).", method, parent.Qualified, strings.Join(fk.Columns, ", "), fk.Name),
				Method:     method,
				ReturnType: parent.TypeName,
				ReturnVar:  parent.VarName,
			})

			// has-many: parent.<Children>() returns the referencing table binding.
			pset := reserve(parent)
			pmethod := unique(pset, goName(child.Name))
			parent.Relations = append(parent.Relations, relationModel{
				Comment:    fmt.Sprintf("// %s returns the %s table that references %s (foreign key %s).", pmethod, childModel.Qualified, parent.Qualified, fk.Name),
				Method:     pmethod,
				ReturnType: childModel.TypeName,
				ReturnVar:  childModel.VarName,
			})
		}
	}
}

func belongsToName(fk schema.ForeignKey, parent schema.Table) string {
	if len(fk.Columns) == 1 {
		col := fk.Columns[0]
		if len(col) > 3 && strings.EqualFold(col[len(col)-3:], "_id") {
			return goName(col[:len(col)-3])
		}
	}
	if parent.Name != "" {
		return goName(singular(parent.Name))
	}
	return goName(singular(fk.RefTable))
}

func tableByName(tables []schema.Table, name string) schema.Table {
	for _, t := range tables {
		if t.Name == name {
			return t
		}
	}
	return schema.Table{}
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
			fb.write(renderRelations(tm))
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
		fb.write(renderRelations(tm))
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
	var buf bytes.Buffer
	buf.WriteString("// Code generated by sqlkitgen. DO NOT EDIT.\n\n")
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

func renderRelations(tm *tableModel) string {
	if len(tm.Relations) == 0 {
		return ""
	}
	return execute(relationsTemplate, tm)
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
		{{.Field}}: sqlkit.NewColumn[{{.GoType}}](info, {{printf "%q" .Name}}{{range .Options}}, {{.}}{{end}}),
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

var relationsTemplate = template.Must(template.New("relations").Parse(`{{- range $i, $r := .Relations}}{{if $i}}
{{end}}
{{$r.Comment}}
func (t {{$.TypeName}}) {{$r.Method}}() {{$r.ReturnType}} { return {{$r.ReturnVar}} }
{{- end}}
`))

var dtoTemplate = template.Must(template.New("dto").Parse("// {{.DTOName}} is a generated row struct for table {{.Qualified}}; any struct\n" +
	"// following Scan's mapping rules works as a destination or Insert value.\n" +
	"type {{.DTOName}} struct {\n" +
	"{{- range .Columns}}\n" +
	"\t{{.Field}} {{.GoType}} `sqlkit:\"{{.Name}}\"`\n" +
	"{{- end}}\n" +
	"}\n"))
