package sqldef

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aita/sqlkit/codegen"
	"github.com/aita/sqlkit/ddl"
	"github.com/aita/sqlkit/meta"
)

// Build is the design's single assembly point: it resolves the portable
// declarations into a meta.Catalog for the dialect ("postgres" or "mysql";
// empty defaults to "postgres"), validating along the way. Column types
// resolve to engine names here, constraint names are assigned here, and each
// table carries its schema in Table.Schema (in declaration order, grouped by
// schema). The returned meta.Catalog is the dialect-agnostic value the codegen
// and ddl generators consume — they never see the builder.
func (c *Design) Build(dialect string) (meta.Catalog, error) {
	if dialect == "" {
		dialect = "postgres"
	}
	if dialect != "postgres" && dialect != "mysql" {
		return meta.Catalog{}, fmt.Errorf("sqldef: unsupported dialect %q (want postgres or mysql)", dialect)
	}

	errs := append([]error(nil), c.errs...)
	fail := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf(format, args...))
	}

	var tables []meta.Table
	for _, s := range c.schemas {
		for _, t := range s.tables {
			tables = append(tables, buildTable(t, c, dialect, fail))
		}
	}
	if len(tables) == 0 {
		fail("catalog declares no tables")
	}

	if len(errs) > 0 {
		return meta.Catalog{}, fmt.Errorf("sqldef: invalid catalog: %w", errors.Join(errs...))
	}
	return meta.Catalog{Tables: tables}, nil
}

func buildTable(t *Table, c *Design, dialect string, fail func(string, ...any)) meta.Table {
	mt := meta.Table{Schema: meta.Schema{Name: t.schema.name}, Name: t.name, Comment: t.comment}
	if len(t.columns) == 0 {
		fail("table %q declares no columns", t.name)
	}
	for _, col := range t.columns {
		engine, ok := col.typ.engineName(dialect)
		if !ok {
			fail("table %q: column %q: type is not available on dialect %s", t.name, col.name, dialect)
		}
		if col.primaryKey && col.nullable {
			fail("table %q: column %q: a primary key column cannot be nullable", t.name, col.name)
		}
		mt.Columns = append(mt.Columns, meta.Column{
			Name:        col.name,
			DataType:    engine,
			Nullable:    col.nullable,
			HasDefault:  col.hasDefault,
			DefaultExpr: col.defaultExpr,
			PrimaryKey:  col.primaryKey,
			Length:      col.typ.length,
			Precision:   col.typ.precision,
			Scale:       col.typ.scale,
			Comment:     col.comment,
		})
	}

	for _, fk := range collectForeignKeys(t) {
		if err := validateForeignKey(t, fk, c); err != nil {
			fail("%v", err)
			continue
		}
		mt.ForeignKeys = append(mt.ForeignKeys, meta.ForeignKey{
			Name:       fmt.Sprintf("fk_%s_%s", t.name, strings.Join(fk.columns, "_")),
			Columns:    fk.columns,
			RefSchema:  meta.Schema{Name: fk.ref.schema.name},
			RefTable:   fk.ref.name,
			RefColumns: fk.refColumns,
		})
	}

	indexNames := map[string]bool{}
	for _, ix := range collectIndexes(t) {
		if len(ix.columns) == 0 {
			fail("table %q: index declares no columns", t.name)
			continue
		}
		ok := true
		for _, name := range ix.columns {
			if !hasColumn(t, name) {
				fail("table %q: index references unknown column %q", t.name, name)
				ok = false
			}
		}
		if !ok {
			continue
		}
		prefix := "idx"
		if ix.unique {
			prefix = "uq"
		}
		name := fmt.Sprintf("%s_%s_%s", prefix, t.name, strings.Join(ix.columns, "_"))
		if indexNames[name] {
			fail("table %q: index %q is declared twice", t.name, name)
			continue
		}
		indexNames[name] = true
		mt.Indexes = append(mt.Indexes, meta.Index{Name: name, Columns: ix.columns, Unique: ix.unique})
	}

	for i, expr := range t.checks {
		if strings.TrimSpace(expr) == "" {
			fail("table %q: Check requires an expression", t.name)
			continue
		}
		mt.Checks = append(mt.Checks, meta.Check{
			Name:       fmt.Sprintf("ck_%s_%d", t.name, i+1),
			Expression: expr,
		})
	}
	return mt
}

// collectForeignKeys merges column-level References with table-level
// ForeignKey declarations, column-level first.
func collectForeignKeys(t *Table) []fkDef {
	var fks []fkDef
	for _, c := range t.columns {
		if c.ref != nil {
			fks = append(fks, fkDef{
				columns:    []string{c.name},
				ref:        c.ref.ref,
				refColumns: c.ref.refColumns,
			})
		}
	}
	return append(fks, t.foreignKeys...)
}

// collectIndexes merges column-level Unique options with table-level
// Index/UniqueIndex declarations, column-level first.
func collectIndexes(t *Table) []indexDef {
	var indexes []indexDef
	for _, c := range t.columns {
		if c.unique {
			indexes = append(indexes, indexDef{columns: []string{c.name}, unique: true})
		}
	}
	return append(indexes, t.indexes...)
}

func validateForeignKey(t *Table, fk fkDef, c *Design) error {
	if fk.ref == nil {
		return fmt.Errorf("table %q: foreign key references a nil table", t.name)
	}
	if fk.ref.schema.design != c {
		return fmt.Errorf("table %q: foreign key references table %q from a different catalog", t.name, fk.ref.name)
	}
	if len(fk.columns) == 0 || len(fk.columns) != len(fk.refColumns) {
		return fmt.Errorf("table %q: foreign key needs matching column lists, got %d local and %d referenced", t.name, len(fk.columns), len(fk.refColumns))
	}
	for _, name := range fk.columns {
		if !hasColumn(t, name) {
			return fmt.Errorf("table %q: foreign key references unknown local column %q", t.name, name)
		}
	}
	for _, name := range fk.refColumns {
		if !hasColumn(fk.ref, name) {
			return fmt.Errorf("table %q: foreign key references unknown column %q of table %q", t.name, name, fk.ref.name)
		}
	}
	return nil
}

func hasColumn(t *Table, name string) bool {
	for _, c := range t.columns {
		if c.name == name {
			return true
		}
	}
	return false
}

// Generate builds the catalog and runs the code generator over its tables,
// producing the typed table bindings (and whatever else opts requests).
// opts.Dialect selects the target dialect (default postgres).
func (c *Design) Generate(opts codegen.Options) ([]codegen.File, error) {
	tables, err := c.Build(opts.Dialect)
	if err != nil {
		return nil, err
	}
	return codegen.Generate(tables, opts)
}

// DDL builds the catalog and renders its tables as CREATE TABLE /
// CREATE INDEX / comment statements for the dialect (default postgres).
func (c *Design) DDL(dialect string) (string, error) {
	tables, err := c.Build(dialect)
	if err != nil {
		return "", err
	}
	return ddl.Generate(tables, dialect)
}

// Write writes generated files into dir, creating it if needed.
func Write(dir string, files []codegen.File) error {
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
