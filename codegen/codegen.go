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
// single file of table bindings with their accessors.
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

	// OmitAccessors suppresses the generated Get and Relations accessors (which
	// need an executor); the bindings and DTOs are still emitted.
	OmitAccessors bool

	// OmitRelations suppresses the foreign-key Relations accessors only.
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

type pkModel struct {
	Field  string
	Param  string
	GoType string
}

type relationModel struct {
	Comment string
	Method  string
	Many    bool
	Target  string // binding var of the related table, used in From()
	Params  []relParam
	Where   string // predicate list rendered inside Where(...)
}

type relParam struct {
	Name   string
	GoType string
}

type tableModel struct {
	TypeName  string
	VarName   string
	DTOName   string
	Schema    string
	Name      string
	Qualified string
	Columns   []columnModel
	PK        []pkModel
	HasPK     bool
	EmitGet   bool
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
		tm.EmitGet = tm.HasPK && !opts.OmitAccessors
		models = append(models, tm)
		index[table.Name] = tm
	}

	if !opts.OmitAccessors && !opts.OmitRelations {
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
		if column.PrimaryKey {
			tm.HasPK = true
			tm.PK = append(tm.PK, pkModel{Field: field, Param: paramName(field), GoType: goType.String()})
		}
	}
	return tm, nil
}

// columnOptions renders the sqlkit.NewColumn functional options for a column.
func columnOptions(column schema.Column) []string {
	var opts []string
	if column.DataType != "" {
		opts = append(opts, fmt.Sprintf("sqlkit.WithSQLType(%q)", column.DataType))
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

// buildRelations turns foreign keys into typed Relations accessors on both the
// referencing table (belongs-to) and the referenced table (has-many).
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

			// belongs-to: childModel.<Parent>(...) loads the referenced row.
			var btWhere []string
			var btParams []relParam
			for i, refCol := range fk.RefColumns {
				pf, pt, ok := columnRef(parent, refCol)
				if !ok {
					btWhere = nil
					break
				}
				param := paramName(goName(fk.Columns[i]))
				param = uniqueParam(btParams, param)
				btParams = append(btParams, relParam{Name: param, GoType: pt})
				btWhere = append(btWhere, fmt.Sprintf("%s.%s.Eq(%s)", parent.VarName, pf, param))
			}
			if len(btWhere) == len(fk.Columns) {
				set := reserve(childModel)
				method := unique(set, belongsToName(fk, parentTable))
				childModel.Relations = append(childModel.Relations, relationModel{
					Comment: fmt.Sprintf("// %s loads the %s row referenced by this row's %s.", method, parent.Qualified, strings.Join(fk.Columns, ", ")),
					Method:  method,
					Many:    false,
					Target:  parent.VarName,
					Params:  btParams,
					Where:   strings.Join(btWhere, ", "),
				})
			}

			// has-many: parentModel.<Children>(...) loads the referencing rows.
			var hmWhere []string
			var hmParams []relParam
			for i, col := range fk.Columns {
				cf, _, ok := columnRef(childModel, col)
				if !ok {
					hmWhere = nil
					break
				}
				_, pt, ok := columnRef(parent, fk.RefColumns[i])
				if !ok {
					hmWhere = nil
					break
				}
				param := paramName(goName(fk.RefColumns[i]))
				param = uniqueParam(hmParams, param)
				hmParams = append(hmParams, relParam{Name: param, GoType: pt})
				hmWhere = append(hmWhere, fmt.Sprintf("%s.%s.Eq(%s)", childModel.VarName, cf, param))
			}
			if len(hmWhere) == len(fk.Columns) {
				set := reserve(parent)
				method := unique(set, goName(child.Name))
				parent.Relations = append(parent.Relations, relationModel{
					Comment: fmt.Sprintf("// %s loads the %s rows that reference this %s row.", method, childModel.Qualified, parent.Qualified),
					Method:  method,
					Many:    true,
					Target:  childModel.VarName,
					Params:  hmParams,
					Where:   strings.Join(hmWhere, ", "),
				})
			}
		}
	}
}

func uniqueParam(existing []relParam, base string) string {
	name := base
	for i := 2; ; i++ {
		clash := false
		for _, p := range existing {
			if p.Name == name {
				clash = true
				break
			}
		}
		if !clash {
			return name
		}
		name = fmt.Sprintf("%s%d", base, i)
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

func columnRef(tm *tableModel, dbName string) (field, goType string, ok bool) {
	for _, c := range tm.Columns {
		if c.Name == dbName {
			return c.Field, c.GoType, true
		}
	}
	return "", "", false
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
			if acc := renderAccessors(tm); acc != "" {
				fb.add(accessorImports(tm)...)
				fb.write(acc)
			}
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
		if acc := renderAccessors(tm); acc != "" {
			fb.add(accessorImports(tm)...)
			fb.write(acc)
		}
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

func accessorImports(tm *tableModel) []string {
	imports := []string{sqlkitImport, "context"}
	needErrors := tm.EmitGet
	for _, r := range tm.Relations {
		if !r.Many {
			needErrors = true
		}
	}
	if needErrors {
		imports = append(imports, "errors")
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

func renderAccessors(tm *tableModel) string {
	if !tm.EmitGet && len(tm.Relations) == 0 {
		return ""
	}
	return execute(accessorsTemplate, tm)
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

var accessorsTemplate = template.Must(template.New("accessors").Parse(`{{if .EmitGet -}}
// Get loads the row with the given primary key into dest — any struct
// following Scan's mapping rules — and reports whether a row was found.
func (t {{.TypeName}}) Get(ctx context.Context, ex sqlkit.Executor{{range .PK}}, {{.Param}} {{.GoType}}{{end}}, dest any) (bool, error) {
	err := ex.Select().From(t).Where({{range $i, $p := .PK}}{{if $i}}, {{end}}t.{{$p.Field}}.Eq({{$p.Param}}){{end}}).Limit(1).All(ctx).Scan(dest)
	if err != nil {
		if errors.Is(err, sqlkit.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
{{end}}
{{- range .Relations}}
{{.Comment}}
{{if .Many -}}
func (t {{$.TypeName}}) {{.Method}}(ctx context.Context, ex sqlkit.Executor{{range .Params}}, {{.Name}} {{.GoType}}{{end}}, dest any) error {
	return ex.Select().From({{.Target}}).Where({{.Where}}).All(ctx).Scan(dest)
}
{{else -}}
func (t {{$.TypeName}}) {{.Method}}(ctx context.Context, ex sqlkit.Executor{{range .Params}}, {{.Name}} {{.GoType}}{{end}}, dest any) (bool, error) {
	err := ex.Select().From({{.Target}}).Where({{.Where}}).Limit(1).All(ctx).Scan(dest)
	if err != nil {
		if errors.Is(err, sqlkit.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
{{end}}
{{- end}}`))

var dtoTemplate = template.Must(template.New("dto").Parse("// {{.DTOName}} is a generated row struct for table {{.Qualified}}; any struct\n" +
	"// following Scan's mapping rules works as a destination or Insert value.\n" +
	"type {{.DTOName}} struct {\n" +
	"{{- range .Columns}}\n" +
	"\t{{.Field}} {{.GoType}} `sqlkit:\"{{.Name}}\"`\n" +
	"{{- end}}\n" +
	"}\n"))
