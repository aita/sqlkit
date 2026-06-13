package sqldef

import (
	"errors"
	"fmt"
	"strings"

	"github.com/aita/sqlkit/ddl"
	"github.com/aita/sqlkit/meta"
)

// Build validates the catalog and returns the assembled meta.Meta. Tables
// are already built (Table assembles each one eagerly), so Build only checks
// for declaration errors; no dialect is needed — column types stay portable
// and the generators resolve them. The returned meta.Meta is the
// dialect-agnostic value the codegen and ddl generators consume.
func (d *Catalog) Build() (meta.Meta, error) {
	errs := append([]error(nil), d.errs...)
	if len(d.tables) == 0 {
		errs = append(errs, errors.New("catalog declares no tables"))
	}
	if len(errs) > 0 {
		return meta.Meta{}, fmt.Errorf("sqldef: invalid catalog: %w", errors.Join(errs...))
	}
	return meta.Meta{Tables: d.tables}, nil
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

func isBlank(s string) bool { return strings.TrimSpace(s) == "" }

// DDL builds the catalog and renders its tables as CREATE TABLE / CREATE INDEX
// / comment statements for the dialect (default postgres).
func (d *Catalog) DDL(dialect string) (string, error) {
	catalog, err := d.Build()
	if err != nil {
		return "", err
	}
	return ddl.Generate(catalog, dialect)
}
