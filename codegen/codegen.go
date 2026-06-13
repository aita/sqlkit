package codegen

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/format"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"text/template"

	"github.com/aita/sqlkit/meta"
)

// File is a single generated source file: a suggested name and its formatted
// contents.
type File struct {
	Name    string
	Content []byte
}

// Write writes generated files into dir, creating it if needed.
func Write(dir string, files []File) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, file := range files {
		if err := os.WriteFile(filepath.Join(dir, file.Name), file.Content, 0o644); err != nil {
			return err
		}
	}
	return nil
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

	// EmitRelations generates the relation-aware surface: foreign-key relation
	// metadata (table.Rels) implementing sqlkit.Relation, Queryer/Q entry points
	// returning a sqlkit.SelectQuery, and a columns/table struct split. Off by
	// default, so the binding stays pure metadata unless asked.
	EmitRelations bool

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
	DTOName func(table meta.Table) string
}

const sqlkitImport = "github.com/aita/sqlkit"

// sqlkitModule is the module path whose version identifies the generator's
// sqlkit release in the generator fingerprint.
const sqlkitModule = "github.com/aita/sqlkit"

// formatVersion is the generated-code format version. Bump it when the binding
// shape or identity scheme changes in a way that should stop bindings generated
// before the change from reconciling with ones generated after it, even though
// the templates' hash already moves with their text.
const formatVersion = "1"

// GeneratorID fingerprints this sqlkit build's code generator: the format
// version, a hash of the generation templates (the source the emitted bindings
// are produced from), and the sqlkit module version. codegen bakes it into each
// binding's TableInfo.Generator. Bindings produced by different sqlkit versions
// or generator code carry different IDs, so a Unit of Work refuses to reconcile
// generated code that drifted across packages even when the structure matches.
func GeneratorID() string {
	h := sha256.New()
	for _, src := range []string{bindingTemplateSrc, relationalBindingTemplateSrc, dtoTemplateSrc} {
		io.WriteString(h, src)
		h.Write([]byte{0})
	}
	templates := hex.EncodeToString(h.Sum(nil))[:12]
	return fmt.Sprintf("fmt%s+tpl%s+sqlkit%s", formatVersion, templates, sqlkitVersion())
}

// sqlkitVersion reports the sqlkit module version of the running generator from
// the build info, falling back to "(devel)" for unversioned builds (a local
// checkout, go run from the module itself, or a test binary).
func sqlkitVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "(devel)"
	}
	if info.Main.Path == sqlkitModule && info.Main.Version != "" {
		return info.Main.Version
	}
	for _, dep := range info.Deps {
		if dep.Path != sqlkitModule {
			continue
		}
		if dep.Replace != nil && dep.Replace.Version != "" {
			return dep.Replace.Version
		}
		if dep.Version != "" {
			return dep.Version
		}
	}
	return "(devel)"
}

type columnModel struct {
	Field      string
	Name       string
	GoType     string
	GoTypeImp  string
	PrimaryKey bool
	HasDefault bool
	// Doc is the column comment as doc-comment lines.
	Doc []string
}

type tableModel struct {
	TypeName  string
	VarName   string
	DTOName   string
	Schema    string
	Name      string
	Qualified string
	Checksum  string
	Generator string
	Columns   []columnModel
	// Doc is the table comment as doc-comment lines.
	Doc []string

	// Relational, when set, switches to the relation-aware binding layout
	// (a columns struct + table struct + Queryer/Q + Rels). It is enabled by
	// Options.EmitRelations.
	Relational  bool
	ColumnsType string // e.g. "UsersColumns"
	Ctor        string // e.g. "newUsersColumns"
	RelsType    string // e.g. "UsersRels"
	Relations   []relationModel
}

// relationModel is one generated relation (a foreign-key join path) on a table.
type relationModel struct {
	Field         string // relation field name in the Rels struct, e.g. "Profile"
	RelType       string // relation struct type name, e.g. "UsersProfileRel"
	TargetColumns string // embedded target columns type, e.g. "ProfilesColumns"
	TargetCtor    string // target columns constructor, e.g. "newProfilesColumns"
	Cardinality   string // "sqlkit.ToOne" or "sqlkit.ToMany"
	JoinOn        string // the JoinOn predicate expression, fully rendered
	// Alias is non-empty for self-referential relations, where the target is an
	// aliased view of the same table (so the self-join is unambiguous). The
	// relation value is then built with TargetCtor().As(Alias).
	Alias string
	// TargetKeys and BaseKeys are the join column pairs by bare SQL name: the
	// relation matches target.TargetKeys[i] = base.BaseKeys[i]. They back Keys.
	TargetKeys []string
	BaseKeys   []string
}

// Generate renders the catalog into one or more Go source files according to
// opts and returns their formatted contents. The md argument is the built
// schema model (meta.Metadata) — produce it with sqldef's Catalog.Build, the
// inspector, or by hand. The dialect (opts.Dialect, default postgres) selects
// the default SQL→Go type table.
func Generate(md meta.Metadata, opts Options) ([]File, error) {
	if opts.PackageName == "" {
		return nil, fmt.Errorf("codegen: package name is required")
	}
	dialect := opts.Dialect
	if dialect == "" {
		dialect = "postgres"
	}
	mapper := opts.TypeMapper
	if mapper == nil {
		mapper = DefaultTypeMapper{}
	}
	dtoName := opts.DTOName
	if dtoName == nil {
		dtoName = defaultDTOName
	}

	tables := filterTables(md.Tables, opts.Tables)
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

	generator := GeneratorID()
	models := make([]*tableModel, 0, len(tables))
	for _, table := range tables {
		tm, err := buildTable(table, dialect, mapper)
		if err != nil {
			return nil, err
		}
		tm.Generator = generator
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

	if opts.EmitRelations {
		relations := buildRelations(tables)
		for i, tm := range models {
			tm.Relational = true
			tm.Relations = relations[qualified(tables[i].Schema.Name, tables[i].Name)]
		}
	}

	return assemble(models, opts)
}

// buildTable builds the binding model for a single table.
func buildTable(table meta.Table, dialect string, mapper TypeMapper) (*tableModel, error) {
	varName := goName(table.Name)
	tm := &tableModel{
		TypeName:    varName + "Table",
		VarName:     varName,
		Schema:      table.Schema.Name,
		Name:        table.Name,
		Qualified:   qualified(table.Schema.Name, table.Name),
		Checksum:    table.Key().Checksum,
		Doc:         docLines(table.Comment),
		ColumnsType: varName + "Columns",
		Ctor:        "new" + varName + "Columns",
		RelsType:    varName + "Rels",
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
			Doc:        docLines(column.Comment),
		}
		tm.Columns = append(tm.Columns, cm)
	}
	return tm, nil
}

// docLines splits a database comment into doc-comment lines.
func docLines(comment string) []string {
	if strings.TrimSpace(comment) == "" {
		return nil
	}
	var lines []string
	for _, line := range strings.Split(comment, "\n") {
		lines = append(lines, strings.TrimRight(line, " \t\r"))
	}
	return lines
}

func defaultDTOName(table meta.Table) string {
	return goName(singular(table.Name))
}

func filterTables(tables []meta.Table, only []string) []meta.Table {
	if len(only) == 0 {
		return tables
	}
	want := map[string]bool{}
	for _, name := range only {
		want[name] = true
	}
	var out []meta.Table
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
	if tm.Relational {
		// sql is referenced only by JoinOn expressions, which are emitted only
		// for tables that actually have relations.
		if len(tm.Relations) > 0 {
			imports = append(imports, sqlImport)
		}
	}
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
	return b.fileWithHeader(pkg, name, "// Code generated by sqlkit. DO NOT EDIT.\n\n")
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
			// An entry may carry an import name ("." or an alias) before the path.
			if name, path, ok := strings.Cut(p, " "); ok {
				buf.WriteString("\t" + name + " \"" + path + "\"\n")
			} else {
				buf.WriteString("\t\"" + p + "\"\n")
			}
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
	if tm.Relational {
		return execute(relationalBindingTemplate, tm)
	}
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

var bindingTemplate = template.Must(template.New("binding").Parse(bindingTemplateSrc))

const bindingTemplateSrc = `// {{.TypeName}} is the typed binding for table {{.Qualified}}.
{{- range .Doc}}
// {{.}}
{{- end}}
type {{.TypeName}} struct {
	info sqlkit.TableInfo
{{- range .Columns}}
{{- range .Doc}}
	// {{.}}
{{- end}}
	{{.Field}} sqlkit.Column[{{.GoType}}]
{{- end}}
}

// {{.VarName}} provides typed access to table {{.Qualified}}.
var {{.VarName}} = func() {{.TypeName}} {
	info := sqlkit.TableInfo{Schema: {{printf "%q" .Schema}}, Name: {{printf "%q" .Name}}, Checksum: {{printf "%q" .Checksum}}, Generator: {{printf "%q" .Generator}}}
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

// As returns a copy of the binding aliased as alias: its columns render
// qualified by the alias and its source renders "{{.Qualified}} AS <alias>".
func (t {{.TypeName}}) As(alias string) {{.TypeName}} {
	info := t.info.WithAlias(alias)
	return {{.TypeName}}{
		info: info,
{{- range .Columns}}
		{{.Field}}: sqlkit.NewColumn[{{.GoType}}](info, {{printf "%q" .Name}}),
{{- end}}
	}
}

var _ sqlkit.Table = {{.VarName}}
`

// relationalBindingTemplate emits the relation-aware binding: a columns struct
// (carrying the Table interface), relation structs and a Rels struct, and a
// table struct embedding the columns plus Queryer/Q entry points. Splitting the
// columns out of the table struct lets relations embed the target's columns
// (so callers reach target columns through the relation) without forming a
// recursive struct type; building every value from the new*Columns constructors
// keeps the package-level vars free of initialization cycles.
var relationalBindingTemplate = template.Must(template.New("relbinding").Parse(relationalBindingTemplateSrc))

const relationalBindingTemplateSrc = `// {{.ColumnsType}} holds the typed column bindings for table {{.Qualified}}.
type {{.ColumnsType}} struct {
	info sqlkit.TableInfo
{{- range .Columns}}
{{- range .Doc}}
	// {{.}}
{{- end}}
	{{.Field}} sqlkit.Column[{{.GoType}}]
{{- end}}
}

func {{.Ctor}}() {{.ColumnsType}} {
	info := sqlkit.TableInfo{Schema: {{printf "%q" .Schema}}, Name: {{printf "%q" .Name}}, Checksum: {{printf "%q" .Checksum}}, Generator: {{printf "%q" .Generator}}}
	return {{.ColumnsType}}{
		info: info,
{{- range .Columns}}
		{{.Field}}: sqlkit.NewColumn[{{.GoType}}](info, {{printf "%q" .Name}}),
{{- end}}
	}
}

// Info returns the table identity.
func (t {{.ColumnsType}}) Info() sqlkit.TableInfo { return t.info }

// SQLSource marks the table as a FROM/JOIN source.
func (t {{.ColumnsType}}) SQLSource() sqlkit.SourceExpression { return t.info.SQLSource() }

// Columns returns all column bindings in declaration order.
func (t {{.ColumnsType}}) Columns() []sqlkit.AnyColumn {
	return []sqlkit.AnyColumn{ {{- range $i, $c := .Columns}}{{if $i}}, {{end}}t.{{$c.Field}}{{end -}} }
}

// PrimaryKey returns the primary key column bindings.
func (t {{.ColumnsType}}) PrimaryKey() []sqlkit.AnyColumn {
	return []sqlkit.AnyColumn{ {{- $first := true}}{{range .Columns}}{{if .PrimaryKey}}{{if not $first}}, {{end}}{{$first = false}}t.{{.Field}}{{end}}{{end -}} }
}

// Defaulted returns the columns with database-side defaults.
func (t {{.ColumnsType}}) Defaulted() []sqlkit.AnyColumn {
	return []sqlkit.AnyColumn{ {{- $first := true}}{{range .Columns}}{{if .HasDefault}}{{if not $first}}, {{end}}{{$first = false}}t.{{.Field}}{{end}}{{end -}} }
}

// As returns a copy of the binding aliased as alias: its columns render
// qualified by the alias and its source renders "{{.Qualified}} AS <alias>".
func (t {{.ColumnsType}}) As(alias string) {{.ColumnsType}} {
	info := t.info.WithAlias(alias)
	return {{.ColumnsType}}{
		info: info,
{{- range .Columns}}
		{{.Field}}: sqlkit.NewColumn[{{.GoType}}](info, {{printf "%q" .Name}}),
{{- end}}
	}
}
{{if .Relations}}
// {{.RelsType}} are the generated relations of table {{.Qualified}}.
type {{.RelsType}} struct {
{{- range .Relations}}
	{{.Field}} {{.RelType}}
{{- end}}
}
{{range .Relations}}
// {{.RelType}} is the {{.Field}} relation of table {{$.Qualified}}.
type {{.RelType}} struct{ {{.TargetColumns}} }

// Target returns the related table joined in.
func (r {{.RelType}}) Target() sqlkit.Table { return r.{{.TargetColumns}} }

// JoinOn is the foreign-key join condition.
func (r {{.RelType}}) JoinOn() sqlkit.Predicate { return {{.JoinOn}} }

// Cardinality reports the relation multiplicity.
func (r {{.RelType}}) Cardinality() sqlkit.Cardinality { return {{.Cardinality}} }

// Name returns the relation's field name.
func (r {{.RelType}}) Name() string { return {{printf "%q" .Field}} }

// Keys returns the join column pairs (target columns, base columns).
func (r {{.RelType}}) Keys() (target, base []string) {
	return []string{ {{- range $i, $c := .TargetKeys}}{{if $i}}, {{end}}{{printf "%q" $c}}{{end -}} }, []string{ {{- range $i, $c := .BaseKeys}}{{if $i}}, {{end}}{{printf "%q" $c}}{{end -}} }
}
{{end}}{{end -}}
// {{.TypeName}} is the typed binding for table {{.Qualified}}.
{{- range .Doc}}
// {{.}}
{{- end}}
type {{.TypeName}} struct {
	{{.ColumnsType}}
{{- if .Relations}}
	Rels {{.RelsType}}
{{- end}}
}

// Queryer starts a SELECT over {{.Qualified}} bound to ex, with all columns
// selected; chain JoinRel, Filter, Where, and the terminals from there.
func (t {{.TypeName}}) Queryer(ex sqlkit.Executor) sqlkit.SelectQuery { return ex.Select().From(t) }

// Q is shorthand for Queryer.
func (t {{.TypeName}}) Q(ex sqlkit.Executor) sqlkit.SelectQuery { return ex.Select().From(t) }

// Relations lists the table's foreign-key relations, so Preload(target) can
// resolve which relation reaches a related table.
func (t {{.TypeName}}) Relations() []sqlkit.Relation {
{{- if .Relations}}
	return []sqlkit.Relation{ {{- range $i, $r := .Relations}}{{if $i}}, {{end}}t.Rels.{{$r.Field}}{{end}} }
{{- else}}
	return nil
{{- end}}
}

// {{.VarName}} provides typed access to table {{.Qualified}}.
var {{.VarName}} = {{.TypeName}}{
	{{.ColumnsType}}: {{.Ctor}}(),
{{- if .Relations}}
	Rels: {{.RelsType}}{
{{- range .Relations}}
{{- if .Alias}}
		{{.Field}}: {{.RelType}}{ {{.TargetColumns}}: {{.TargetCtor}}().As({{printf "%q" .Alias}}) },
{{- else}}
		{{.Field}}: {{.RelType}}{ {{.TargetColumns}}: {{.TargetCtor}}() },
{{- end}}
{{- end}}
	},
{{- end}}
}

var _ sqlkit.Table = {{.VarName}}
`

var dtoTemplate = template.Must(template.New("dto").Parse(dtoTemplateSrc))

const dtoTemplateSrc = "// {{.DTOName}} is a generated row struct for table {{.Qualified}}; any struct\n" +
	"// following Scan's mapping rules works as a destination or Insert value.\n" +
	"{{- range .Doc}}\n" +
	"// {{.}}\n" +
	"{{- end}}\n" +
	"type {{.DTOName}} struct {\n" +
	"{{- range .Columns}}\n" +
	"{{- range .Doc}}\n" +
	"\t// {{.}}\n" +
	"{{- end}}\n" +
	"\t{{.Field}} {{.GoType}} `sqlkit:\"{{.Name}}\"`\n" +
	"{{- end}}\n" +
	"}\n"
