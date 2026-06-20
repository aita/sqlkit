package sqlite

import (
	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse"
	rsql "github.com/rqlite/sql"
)

func convertCreateTable(s *rsql.CreateTableStatement) (sql.Query, error) {
	// CREATE TABLE ... AS SELECT is a distinct shape not modeled by the builder.
	if s.Select != nil {
		return nil, sqlparse.Unsupported("CREATE TABLE ... AS SELECT")
	}

	b := sql.CreateTable(tableName(s.Schema, s.Name))
	if s.IfNotExists.IsValid() {
		b = b.IfNotExists()
	}

	var primaryKey []string
	for _, cd := range s.Columns {
		col, pk, err := convertColumnDef(cd)
		if err != nil {
			return nil, err
		}
		b = b.Column(col)
		if pk {
			primaryKey = append(primaryKey, col.Name.String())
		}
	}

	for _, c := range s.Constraints {
		var err error
		if b, primaryKey, err = applyTableConstraint(b, primaryKey, c); err != nil {
			return nil, err
		}
	}

	if len(primaryKey) > 0 {
		b = b.PrimaryKey(primaryKey...)
	}
	return b, nil
}

// convertColumnDef converts a column definition, reporting whether the column is
// a (non-autoincrement) PRIMARY KEY whose name must join the table's primary-key
// list. An autoincrement primary key renders inline via ColumnDef.AutoIncrement,
// so it is not added to that list.
func convertColumnDef(cd *rsql.ColumnDefinition) (sql.ColumnDef, bool, error) {
	if cd.Type == nil {
		return sql.ColumnDef{}, false, sqlparse.Unsupported("column without a type")
	}
	col := sql.ColumnDef{
		Name: sql.NewIdentAt(cd.Name.Name, offsetOf(cd.Name.NamePos)),
		Type: sql.RawType(cd.Type.String()),
	}

	primaryKey := false
	for _, c := range cd.Constraints {
		switch con := c.(type) {
		case *rsql.NotNullConstraint:
			col.NotNull = true
		case *rsql.PrimaryKeyConstraint:
			// A column-level PRIMARY KEY joins the table's primary-key list so it
			// renders as an explicit constraint.
			primaryKey = true
			if con.Autoincrement.IsValid() {
				col.AutoIncrement = true
				// AUTOINCREMENT renders through the dialect, which only acts on an
				// EngineType (RawType is opaque to it).
				col.Type = sql.EngineType{Name: cd.Type.Name.Name}
			}
		case *rsql.UniqueConstraint:
			col.Unique = true
		case *rsql.DefaultConstraint:
			// A DEFAULT must be static SQL (no bind values), so render the literal
			// or expression verbatim.
			col.Default = sql.Raw(con.Expr.String())
		default:
			return sql.ColumnDef{}, false, sqlparse.Unsupportedf("column constraint %T", c)
		}
	}
	return col, primaryKey, nil
}

func applyTableConstraint(b sql.CreateTableBuilder, primaryKey []string, c rsql.Constraint) (sql.CreateTableBuilder, []string, error) {
	switch con := c.(type) {
	case *rsql.PrimaryKeyConstraint:
		for _, col := range con.Columns {
			primaryKey = append(primaryKey, col.Name)
		}
		return b, primaryKey, nil
	case *rsql.UniqueConstraint:
		names := make([]string, 0, len(con.Columns))
		for _, col := range con.Columns {
			ident, ok := col.X.(*rsql.Ident)
			if !ok {
				return b, primaryKey, sqlparse.Unsupported("UNIQUE on an expression")
			}
			names = append(names, ident.Name)
		}
		return b.Unique(constraintName(con.Name), names...), primaryKey, nil
	case *rsql.CheckConstraint:
		return b.Check(constraintName(con.Name), sql.Raw(con.Expr.String())), primaryKey, nil
	default:
		return b, primaryKey, sqlparse.Unsupportedf("table constraint %T", c)
	}
}

func constraintName(name *rsql.Ident) string {
	if name == nil {
		return ""
	}
	return name.Name
}

func convertCreateIndex(s *rsql.CreateIndexStatement) (sql.Query, error) {
	// A partial index (WHERE) is not modeled.
	if s.WhereExpr != nil {
		return nil, sqlparse.Unsupported("partial index (CREATE INDEX ... WHERE)")
	}
	columns := make([]string, 0, len(s.Columns))
	for _, col := range s.Columns {
		ident, ok := col.X.(*rsql.Ident)
		if !ok {
			return nil, sqlparse.Unsupported("index on an expression")
		}
		columns = append(columns, ident.Name)
	}

	b := sql.CreateIndex(s.Name.Name, tableName(s.Schema, s.Table), columns...)
	if s.Unique.IsValid() {
		b = b.Unique()
	}
	if s.IfNotExists.IsValid() {
		b = b.IfNotExists()
	}
	return b, nil
}

func convertDropTable(s *rsql.DropTableStatement) (sql.Query, error) {
	b := sql.DropTable(tableName(s.Schema, s.Name))
	if s.IfExists.IsValid() {
		b = b.IfExists()
	}
	return b, nil
}
