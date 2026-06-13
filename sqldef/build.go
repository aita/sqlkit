package sqldef

import (
	"errors"
	"fmt"
	"strings"

	"github.com/aita/sqlkit/meta"
)

// Build validates the catalog and returns the assembled meta.Metadata. Tables
// are already built (Table assembles each one eagerly), so Build only checks
// for declaration errors; no dialect is needed — column types stay portable
// and the generators resolve them. The returned meta.Metadata is the
// dialect-agnostic value the codegen generator and the DDL renderer consume.
func (d *Catalog) Build() (meta.Metadata, error) {
	errs := append([]error(nil), d.errs...)
	if len(d.tables) == 0 {
		errs = append(errs, errors.New("catalog declares no tables"))
	}
	if len(errs) > 0 {
		return meta.Metadata{}, fmt.Errorf("sqldef: invalid catalog: %w", errors.Join(errs...))
	}
	return meta.Metadata{Tables: d.tables}, nil
}

// BuildTable assembles a single meta.Table from sqldef table elements without a
// catalog, validating it. It is the reuse point for tools that build one table
// at a time — such as migration operations — so they share the sqldef
// vocabulary (Column, Index, UniqueIndex, Check, PrimaryKey, TableComment) and
// its validation. Foreign keys declared with References/ForeignKey need their
// target table in a catalog, so name-based foreign keys belong in the caller
// (e.g. the migrate operator) instead.
func BuildTable(schema, name string, elements ...TableElement) (meta.Table, error) {
	cat := NewCatalog(DefaultSchema(schema))
	table := cat.Table(name, elements...)
	if _, err := cat.Build(); err != nil {
		return meta.Table{}, err
	}
	return table, nil
}

// BuildColumn assembles a single meta.Column from one sqldef Column element, for
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
		return meta.Column{}, fmt.Errorf("sqldef: invalid column: %w", errors.Join(cat.errs...))
	}
	if len(b.columns) != 1 {
		return meta.Column{}, fmt.Errorf("sqldef: expected exactly one column, got %d", len(b.columns))
	}
	c := b.columns[0]
	if c.primaryKey && c.nullable {
		return meta.Column{}, fmt.Errorf("sqldef: column %q: a primary key column cannot be nullable", c.name)
	}
	return c.toMeta(), nil
}

// resolveFK validates a foreign key against the local columns and the
// referenced table, assigns its name, and returns the meta.ForeignKey. It
// reports failures to the catalog and returns ok=false on error.
func (d *Catalog) resolveFK(table string, columns []meta.Column, fk fkDef) (meta.ForeignKey, bool) {
	if fk.ref.Name == "" {
		d.fail("table %q: foreign key references an unnamed table", table)
		return meta.ForeignKey{}, false
	}
	if !d.registered[tableKey(fk.ref.Schema.Name, fk.ref.Name)] {
		d.fail("table %q: foreign key references table %q which is not declared in this catalog", table, fk.ref.Name)
		return meta.ForeignKey{}, false
	}
	if len(fk.columns) == 0 || len(fk.columns) != len(fk.refColumns) {
		d.fail("table %q: foreign key needs matching column lists, got %d local and %d referenced", table, len(fk.columns), len(fk.refColumns))
		return meta.ForeignKey{}, false
	}
	for _, name := range fk.columns {
		if !hasMetaColumn(columns, name) {
			d.fail("table %q: foreign key references unknown local column %q", table, name)
			return meta.ForeignKey{}, false
		}
	}
	for _, name := range fk.refColumns {
		if !hasMetaColumn(fk.ref.Columns, name) {
			d.fail("table %q: foreign key references unknown column %q of table %q", table, name, fk.ref.Name)
			return meta.ForeignKey{}, false
		}
	}
	return meta.ForeignKey{
		Name:       fmt.Sprintf("fk_%s_%s", table, joinCols(fk.columns)),
		Columns:    fk.columns,
		RefSchema:  fk.ref.Schema,
		RefTable:   fk.ref.Name,
		RefColumns: fk.refColumns,
		OnDelete:   fk.onDelete,
		OnUpdate:   fk.onUpdate,
	}, true
}

// collectForeignKeys merges column-level References with table-level
// ForeignKey declarations, column-level first.
func collectForeignKeys(b *tableBuilder) []fkDef {
	var fks []fkDef
	for _, c := range b.columns {
		if c.ref != nil {
			fks = append(fks, fkDef{
				columns:    []string{c.name},
				ref:        c.ref.ref,
				refColumns: c.ref.refColumns,
				onDelete:   c.ref.onDelete,
				onUpdate:   c.ref.onUpdate,
			})
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
