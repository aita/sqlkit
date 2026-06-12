package dsl

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aita/sqlkit/codegen"
	"github.com/aita/sqlkit/schema"
)

// Build resolves the design into the schema model for the dialect ("postgres"
// or "mysql"; empty defaults to "postgres"), validating it along the way.
func (s *SchemaExpr) Build(dialect string) (schema.Schema, error) {
	if dialect == "" {
		dialect = "postgres"
	}
	if dialect != "postgres" && dialect != "mysql" {
		return schema.Schema{}, fmt.Errorf("dsl: unsupported dialect %q (want postgres or mysql)", dialect)
	}

	errs := append([]error(nil), s.errs...)
	fail := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf(format, args...))
	}

	if len(s.tables) == 0 {
		fail("schema %q declares no tables", s.name)
	}

	byName := make(map[string]*tableExpr, len(s.tables))
	for _, t := range s.tables {
		byName[t.name] = t
	}

	tables := make([]schema.Table, 0, len(s.tables))
	for _, t := range s.tables {
		st := schema.Table{Schema: s.name, Name: t.name}
		if len(t.columns) == 0 {
			fail("table %q declares no columns", t.name)
		}
		for _, c := range t.columns {
			engine, ok := c.typ.engineName(dialect)
			if !ok {
				fail("table %q: column %q: type is not available on dialect %s", t.name, c.name, dialect)
			}
			if c.primaryKey && c.nullable {
				fail("table %q: column %q: a primary key column cannot be nullable", t.name, c.name)
			}
			st.Columns = append(st.Columns, schema.Column{
				Name:       c.name,
				DataType:   engine,
				Nullable:   c.nullable,
				HasDefault: c.hasDefault,
				PrimaryKey: c.primaryKey,
				Length:     c.typ.length,
				Precision:  c.typ.precision,
				Scale:      c.typ.scale,
			})
		}

		for _, fk := range t.collectForeignKeys() {
			if err := validateForeignKey(t, fk, byName); err != nil {
				errs = append(errs, err)
				continue
			}
			st.ForeignKeys = append(st.ForeignKeys, schema.ForeignKey{
				Name:       fmt.Sprintf("fk_%s_%s", t.name, strings.Join(fk.columns, "_")),
				Columns:    fk.columns,
				RefSchema:  s.name,
				RefTable:   fk.refTable,
				RefColumns: fk.refColumns,
			})
		}
		tables = append(tables, st)
	}

	if len(errs) > 0 {
		return schema.Schema{}, fmt.Errorf("dsl: invalid design: %w", errors.Join(errs...))
	}
	return schema.Schema{Name: s.name, Dialect: dialect, Tables: tables}, nil
}

// collectForeignKeys merges column-level References with table-level
// ForeignKey declarations, column-level first.
func (t *tableExpr) collectForeignKeys() []fkExpr {
	var fks []fkExpr
	for _, c := range t.columns {
		if c.ref != nil {
			fks = append(fks, fkExpr{
				columns:    []string{c.name},
				refTable:   c.ref.table,
				refColumns: c.ref.columns,
			})
		}
	}
	return append(fks, t.foreignKeys...)
}

func validateForeignKey(t *tableExpr, fk fkExpr, byName map[string]*tableExpr) error {
	if len(fk.columns) == 0 || len(fk.columns) != len(fk.refColumns) {
		return fmt.Errorf("table %q: foreign key needs matching column lists, got %d local and %d referenced", t.name, len(fk.columns), len(fk.refColumns))
	}
	for _, name := range fk.columns {
		if !hasColumn(t, name) {
			return fmt.Errorf("table %q: foreign key references unknown local column %q", t.name, name)
		}
	}
	ref, ok := byName[fk.refTable]
	if !ok {
		return fmt.Errorf("table %q: foreign key references unknown table %q", t.name, fk.refTable)
	}
	for _, name := range fk.refColumns {
		if !hasColumn(ref, name) {
			return fmt.Errorf("table %q: foreign key references unknown column %q of table %q", t.name, name, fk.refTable)
		}
	}
	return nil
}

func hasColumn(t *tableExpr, name string) bool {
	for _, c := range t.columns {
		if c.name == name {
			return true
		}
	}
	return false
}

// Generate builds the design and runs the code generator on it, producing
// the typed table bindings (and whatever else opts requests). opts.Dialect
// selects the target dialect (default postgres).
func (s *SchemaExpr) Generate(opts codegen.Options) ([]codegen.File, error) {
	model, err := s.Build(opts.Dialect)
	if err != nil {
		return nil, err
	}
	return codegen.Generate(model, opts)
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
