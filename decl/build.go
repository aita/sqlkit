package decl

import (
	"errors"
	"fmt"
	"strings"

	"github.com/aita/sqlkit/meta"
)

// Build resolves the catalog's foreign keys, validates it, and returns the
// assembled meta.Metadata. Columns, indexes, and checks are assembled eagerly
// by Table; foreign keys are resolved here, once every table is known, so they
// may target a table declared later or the table itself. No dialect is needed —
// column types stay portable and the generators resolve them. Build does not
// mutate the catalog, so it may be called more than once. The returned
// meta.Metadata is the dialect-agnostic value the codegen generator and the DDL
// renderer consume.
func (d *Catalog) Build() (meta.Metadata, error) {
	errs := append([]error(nil), d.errs...)

	// Resolve foreign keys into a copy of the tables so Build stays pure: a table
	// starts with no foreign keys, so the first append allocates a fresh slice
	// and the catalog's own tables are never touched.
	tables := make([]meta.Table, len(d.tables))
	copy(tables, d.tables)
	for _, p := range d.pendingFKs {
		t := &tables[p.table]
		fk, ferrs := d.resolveFK(t.Schema.Name, t.Name, t.Columns, p.def)
		if len(ferrs) > 0 {
			errs = append(errs, ferrs...)
			continue
		}
		t.ForeignKeys = append(t.ForeignKeys, fk)
	}

	if len(tables) == 0 && len(d.extensions) == 0 && len(d.enums) == 0 &&
		len(d.sequences) == 0 && len(d.matviews) == 0 && len(d.grants) == 0 {
		errs = append(errs, errors.New("catalog declares no objects"))
	}
	if len(errs) > 0 {
		return meta.Metadata{}, fmt.Errorf("decl: invalid catalog: %w", errors.Join(errs...))
	}
	return meta.Metadata{
		Tables:            tables,
		Extensions:        append([]meta.Extension(nil), d.extensions...),
		Enums:             append([]meta.Enum(nil), d.enums...),
		Sequences:         append([]meta.Sequence(nil), d.sequences...),
		MaterializedViews: append([]meta.MaterializedView(nil), d.matviews...),
		Grants:            append([]meta.Grant(nil), d.grants...),
	}, nil
}

// BuildTable assembles a single meta.Table from decl table elements without a
// catalog, validating it. It is the reuse point for tools that build one table
// at a time — such as migration operations — so they share the decl
// vocabulary (Column, Index, UniqueIndex, Check, PrimaryKey, TableComment) and
// its validation. Foreign keys declared with References/ForeignKey need their
// target table in a catalog, so name-based foreign keys belong in the caller
// (e.g. the migrate operator) instead.
func BuildTable(schema, name string, elements ...TableElement) (meta.Table, error) {
	cat := NewCatalog(DefaultSchema(schema))
	cat.Table(name, elements...)
	md, err := cat.Build()
	if err != nil {
		return meta.Table{}, err
	}
	// Build resolves foreign keys (e.g. a self-reference), so return the
	// assembled table from the metadata rather than the value Table returned
	// before resolution.
	return md.Tables[0], nil
}

// BuildColumn assembles a single meta.Column from one decl Column element, for
// operations that take a lone column such as ALTER TABLE ADD COLUMN. The element
// must declare exactly one column; column-level Unique/References (table-level
// constructs) carry no meaning on a lone column and are dropped.
func BuildColumn(element TableElement) (meta.Column, error) {
	// Apply the element to a scratch builder and read the column directly, rather
	// than calling Build, so table-level constructs the element may carry
	// (column Unique/References, which produce an index or a foreign key against
	// a table not declared here) are dropped instead of failing validation.
	cat := NewCatalog()
	b := &tableBuilder{catalog: cat, name: "_column"}
	element.applyTable(b)
	if len(cat.errs) > 0 {
		return meta.Column{}, fmt.Errorf("decl: invalid column: %w", errors.Join(cat.errs...))
	}
	if len(b.columns) != 1 {
		return meta.Column{}, fmt.Errorf("decl: expected exactly one column, got %d", len(b.columns))
	}
	c := b.columns[0]
	if c.primaryKey && c.nullable {
		return meta.Column{}, fmt.Errorf("decl: column %q: a primary key column cannot be nullable", c.name)
	}
	return c.toMeta(), nil
}

// resolveFK looks the foreign key's named target up in the catalog, validates
// it against the local columns and the referenced table, assigns its name, and
// returns the meta.ForeignKey. fromSchema is the referencing table's schema,
// used to resolve an unqualified target. It returns the declaration errors it
// finds (empty on success) rather than reporting to the catalog, so Build stays
// pure and idempotent.
func (d *Catalog) resolveFK(fromSchema, table string, columns []meta.Column, fk fkDef) (meta.ForeignKey, []error) {
	fail := func(format string, args ...any) []error {
		return []error{fmt.Errorf("table %q: "+format, append([]any{table}, args...)...)}
	}
	if fk.refTable == "" {
		return meta.ForeignKey{}, fail("foreign key references an unnamed table")
	}
	refSchema := fromSchema
	if fk.refQualified {
		refSchema = fk.refSchema
	}
	idx, ok := d.tableIndex[tableKey(refSchema, fk.refTable)]
	if !ok {
		return meta.ForeignKey{}, fail("foreign key references table %q which is not declared in this catalog", refDisplay(fk))
	}
	ref := d.tables[idx]
	if len(fk.columns) == 0 || len(fk.columns) != len(fk.refColumns) {
		return meta.ForeignKey{}, fail("foreign key needs matching column lists, got %d local and %d referenced", len(fk.columns), len(fk.refColumns))
	}
	for _, name := range fk.columns {
		if !hasMetaColumn(columns, name) {
			return meta.ForeignKey{}, fail("foreign key references unknown local column %q", name)
		}
	}
	for _, name := range fk.refColumns {
		if !hasMetaColumn(ref.Columns, name) {
			return meta.ForeignKey{}, fail("foreign key references unknown column %q of table %q", name, fk.refTable)
		}
	}
	return meta.ForeignKey{
		Name:       fmt.Sprintf("fk_%s_%s", table, joinCols(fk.columns)),
		Columns:    fk.columns,
		RefSchema:  ref.Schema,
		RefTable:   fk.refTable,
		RefColumns: fk.refColumns,
		OnDelete:   fk.onDelete,
		OnUpdate:   fk.onUpdate,
	}, nil
}

// refDisplay renders a foreign key's target as the user named it, for error
// messages: "schema.table" when the schema was given, bare "table" otherwise.
func refDisplay(fk fkDef) string {
	if fk.refQualified {
		return fk.refSchema + "." + fk.refTable
	}
	return fk.refTable
}

// collectForeignKeys merges column-level References with table-level
// ForeignKey declarations, column-level first.
func collectForeignKeys(b *tableBuilder) []fkDef {
	var fks []fkDef
	for _, c := range b.columns {
		if c.ref != nil {
			fk := *c.ref
			fk.columns = []string{c.name}
			fks = append(fks, fk)
		}
	}
	return append(fks, b.foreignKeys...)
}

// collectIndexes merges column-level Unique options with table-level
// Index/UniqueIndex declarations, column-level first.
func collectIndexes(b *tableBuilder) []indexDef {
	var indexes []indexDef
	for _, c := range b.columns {
		if c.unique {
			indexes = append(indexes, indexDef{columns: []string{c.name}, unique: true})
		}
	}
	return append(indexes, b.indexes...)
}

func hasMetaColumn(columns []meta.Column, name string) bool {
	for _, c := range columns {
		if c.Name == name {
			return true
		}
	}
	return false
}

func joinCols(columns []string) string { return strings.Join(columns, "_") }
