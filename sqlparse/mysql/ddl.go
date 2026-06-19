package mysql

import (
	"strconv"
	"strings"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/types"
)

func convertCreateTable(s *ast.CreateTableStmt) (sql.Query, error) {
	if s.TemporaryKeyword == ast.TemporaryGlobal {
		return nil, sqlparse.Unsupported("CREATE GLOBAL TEMPORARY TABLE")
	}
	if s.ReferTable != nil || s.Select != nil {
		return nil, sqlparse.Unsupported("CREATE TABLE ... LIKE / AS SELECT")
	}
	if len(s.Options) > 0 {
		// Table options (ENGINE, CHARSET, AUTO_INCREMENT=, ...) have no typed
		// home, so modeling the table would silently drop them; round-trip raw.
		return nil, sqlparse.Unsupported("CREATE TABLE options")
	}

	ct := sql.CreateTable(tableFromName(s.Table))
	if s.IfNotExists {
		ct = ct.IfNotExists()
	}
	if s.TemporaryKeyword == ast.TemporaryLocal {
		ct = ct.Temporary()
	}

	var primaryKey []string
	for _, cd := range s.Cols {
		col, isPK, err := convertColumnDef(cd)
		if err != nil {
			return nil, err
		}
		ct = ct.Column(col)
		if isPK {
			primaryKey = append(primaryKey, col.Name)
		}
	}

	for _, con := range s.Constraints {
		switch con.Tp {
		case ast.ConstraintPrimaryKey:
			keys, err := constraintKeys(con)
			if err != nil {
				return nil, err
			}
			primaryKey = keys
		case ast.ConstraintUniq, ast.ConstraintUniqKey, ast.ConstraintUniqIndex:
			keys, err := constraintKeys(con)
			if err != nil {
				return nil, err
			}
			ct = ct.Unique(con.Name, keys...)
		case ast.ConstraintForeignKey:
			fk, err := convertForeignKey(con)
			if err != nil {
				return nil, err
			}
			ct = ct.ForeignKey(fk)
		case ast.ConstraintCheck:
			expr, err := staticExpr(con.Expr)
			if err != nil {
				return nil, err
			}
			ct = ct.Check(con.Name, expr)
		case ast.ConstraintKey, ast.ConstraintIndex:
			keys, err := constraintKeys(con)
			if err != nil {
				return nil, err
			}
			ct = ct.Index(con.Name, keys...)
		default:
			return nil, sqlparse.Unsupportedf("table constraint %d", con.Tp)
		}
	}

	if len(primaryKey) > 0 {
		ct = ct.PrimaryKey(primaryKey...)
	}
	return ct, nil
}

// convertColumnDef builds a ColumnDef and reports whether an inline PRIMARY KEY
// option marks it as the table's primary key.
func convertColumnDef(cd *ast.ColumnDef) (sql.ColumnDef, bool, error) {
	def := sql.ColumnDef{Name: cd.Name.Name.O}

	var isPK, autoInc bool
	for _, opt := range cd.Options {
		switch opt.Tp {
		case ast.ColumnOptionPrimaryKey:
			isPK = true
		case ast.ColumnOptionNotNull:
			def.NotNull = true
		case ast.ColumnOptionNull:
			// Explicit NULL is the default; nothing to record.
		case ast.ColumnOptionAutoIncrement:
			autoInc = true
		case ast.ColumnOptionUniqKey:
			def.Unique = true
		case ast.ColumnOptionDefaultValue:
			expr, err := staticExpr(opt.Expr)
			if err != nil {
				return sql.ColumnDef{}, false, err
			}
			def.Default = expr
		case ast.ColumnOptionComment:
			comment, err := stringLiteral(opt.Expr)
			if err != nil {
				return sql.ColumnDef{}, false, err
			}
			def.Comment = comment
		case ast.ColumnOptionReference:
			ref, err := convertColumnReference(opt.Refer)
			if err != nil {
				return sql.ColumnDef{}, false, err
			}
			def.References = ref
		case ast.ColumnOptionCollate:
			def.Collate = opt.StrValue
		case ast.ColumnOptionOnUpdate:
			expr, err := staticExpr(opt.Expr)
			if err != nil {
				return sql.ColumnDef{}, false, err
			}
			def.OnUpdate = expr
		case ast.ColumnOptionGenerated:
			expr, err := staticExpr(opt.Expr)
			if err != nil {
				return sql.ColumnDef{}, false, err
			}
			def.Generated = &sql.GeneratedColumn{Expression: expr, Stored: opt.Stored}
		default:
			return sql.ColumnDef{}, false, sqlparse.Unsupportedf("column option %d", opt.Tp)
		}
	}

	def.AutoIncrement = autoInc
	def.Type = columnType(cd.Tp, autoInc)
	return def, isPK, nil
}

// columnType maps a parsed field type to a sqlkit ColumnType. Auto-incrementing
// integers use EngineType so the dialect can render the AUTO_INCREMENT clause;
// every other column keeps its verbatim MySQL type text.
func columnType(ft *types.FieldType, autoInc bool) sql.ColumnType {
	if autoInc {
		return sql.EngineType{Name: strings.ToLower(types.TypeStr(ft.GetType()))}
	}
	return sql.RawType(ft.InfoSchemaStr())
}

func constraintKeys(con *ast.Constraint) ([]string, error) {
	keys := make([]string, 0, len(con.Keys))
	for _, key := range con.Keys {
		if key.Expr != nil {
			return nil, sqlparse.Unsupported("functional index key")
		}
		if key.Column == nil {
			return nil, sqlparse.Unsupported("constraint key without a column")
		}
		keys = append(keys, key.Column.Name.O)
	}
	return keys, nil
}

func convertColumnReference(ref *ast.ReferenceDef) (*sql.ColumnReference, error) {
	if ref == nil || ref.Table == nil {
		return nil, sqlparse.Unsupported("foreign key without referenced table")
	}
	cols, err := referenceColumns(ref)
	if err != nil {
		return nil, err
	}
	out := &sql.ColumnReference{
		Schema:   ref.Table.Schema.O,
		Table:    ref.Table.Name.O,
		OnDelete: deleteAction(ref.OnDelete),
		OnUpdate: updateAction(ref.OnUpdate),
	}
	switch len(cols) {
	case 0:
	case 1:
		out.Column = cols[0]
	default:
		return nil, sqlparse.Unsupported("multi-column column-level foreign key")
	}
	return out, nil
}

func convertForeignKey(con *ast.Constraint) (sql.ForeignKeyConstraint, error) {
	if con.Refer == nil || con.Refer.Table == nil {
		return sql.ForeignKeyConstraint{}, sqlparse.Unsupported("foreign key without referenced table")
	}
	columns, err := constraintKeys(con)
	if err != nil {
		return sql.ForeignKeyConstraint{}, err
	}
	refColumns, err := referenceColumns(con.Refer)
	if err != nil {
		return sql.ForeignKeyConstraint{}, err
	}
	return sql.ForeignKeyConstraint{
		Name:       con.Name,
		Columns:    columns,
		RefSchema:  con.Refer.Table.Schema.O,
		RefTable:   con.Refer.Table.Name.O,
		RefColumns: refColumns,
		OnDelete:   deleteAction(con.Refer.OnDelete),
		OnUpdate:   updateAction(con.Refer.OnUpdate),
	}, nil
}

func referenceColumns(ref *ast.ReferenceDef) ([]string, error) {
	cols := make([]string, 0, len(ref.IndexPartSpecifications))
	for _, spec := range ref.IndexPartSpecifications {
		if spec.Column == nil {
			return nil, sqlparse.Unsupported("foreign key reference without a column")
		}
		cols = append(cols, spec.Column.Name.O)
	}
	return cols, nil
}

// deleteAction / updateAction map a referential action to its SQL clause text.
// An absent clause (and the no-option default) render nothing.
func deleteAction(o *ast.OnDeleteOpt) string {
	if o == nil {
		return ""
	}
	return o.ReferOpt.String()
}

func updateAction(o *ast.OnUpdateOpt) string {
	if o == nil {
		return ""
	}
	return o.ReferOpt.String()
}

func convertAlterTable(s *ast.AlterTableStmt) (sql.Query, error) {
	// DROP FOREIGN KEY / DROP INDEX are their own statement types in sqlkit, not
	// AlterTable alterations, so map them only when they stand alone. Combined
	// with other actions they fall through to AlterTable and round-trip raw.
	if len(s.Specs) == 1 {
		switch spec := s.Specs[0]; spec.Tp {
		case ast.AlterTableDropForeignKey:
			return sql.DropForeignKey(tableFromName(s.Table), spec.Name), nil
		case ast.AlterTableDropIndex:
			return sql.DropIndex(spec.Name, tableFromName(s.Table)), nil
		}
	}

	at := sql.AlterTable(tableFromName(s.Table))
	for _, spec := range s.Specs {
		var err error
		if at, err = applyAlterSpec(at, spec); err != nil {
			return nil, err
		}
	}
	return at, nil
}

func applyAlterSpec(at sql.AlterTableBuilder, spec *ast.AlterTableSpec) (sql.AlterTableBuilder, error) {
	switch spec.Tp {
	case ast.AlterTableAddColumns:
		for _, cd := range spec.NewColumns {
			col, _, err := convertColumnDef(cd)
			if err != nil {
				return at, err
			}
			at = at.AddColumn(col)
		}
		return at, nil
	case ast.AlterTableDropColumn:
		return at.DropColumn(spec.OldColumnName.Name.O), nil
	case ast.AlterTableModifyColumn:
		col, _, err := convertColumnDef(spec.NewColumns[0])
		if err != nil {
			return at, err
		}
		return at.RestateColumn(col), nil
	case ast.AlterTableChangeColumn:
		// CHANGE COLUMN renames and restates at once; sqlkit can restate but not
		// rename in one action, so only a no-rename CHANGE maps cleanly.
		col, _, err := convertColumnDef(spec.NewColumns[0])
		if err != nil {
			return at, err
		}
		if spec.OldColumnName.Name.O != col.Name {
			return at, sqlparse.Unsupported("CHANGE COLUMN that renames a column")
		}
		return at.RestateColumn(col), nil
	case ast.AlterTableRenameColumn:
		return at.RenameColumn(spec.OldColumnName.Name.O, spec.NewColumnName.Name.O), nil
	case ast.AlterTableDropPrimaryKey:
		return at.DropPrimaryKey(), nil
	case ast.AlterTableRenameTable:
		return at.RenameTo(spec.NewTable.Name.O), nil
	case ast.AlterTableAddConstraint:
		return applyAddConstraint(at, spec.Constraint)
	default:
		return at, sqlparse.Unsupportedf("ALTER TABLE action %d", spec.Tp)
	}
}

func applyAddConstraint(at sql.AlterTableBuilder, con *ast.Constraint) (sql.AlterTableBuilder, error) {
	if con == nil {
		return at, sqlparse.Unsupported("empty ADD CONSTRAINT")
	}
	switch con.Tp {
	case ast.ConstraintPrimaryKey:
		keys, err := constraintKeys(con)
		if err != nil {
			return at, err
		}
		return at.AddPrimaryKey(keys...), nil
	case ast.ConstraintUniq, ast.ConstraintUniqKey, ast.ConstraintUniqIndex:
		keys, err := constraintKeys(con)
		if err != nil {
			return at, err
		}
		return at.AddUnique(con.Name, keys...), nil
	case ast.ConstraintForeignKey:
		fk, err := convertForeignKey(con)
		if err != nil {
			return at, err
		}
		return at.AddForeignKey(fk), nil
	case ast.ConstraintCheck:
		expr, err := staticExpr(con.Expr)
		if err != nil {
			return at, err
		}
		return at.AddCheck(con.Name, expr), nil
	default:
		return at, sqlparse.Unsupportedf("ADD CONSTRAINT %d", con.Tp)
	}
}

func convertDropTable(s *ast.DropTableStmt) (sql.Query, error) {
	if s.IsView {
		// sqlkit's DropView drops a single view; a multi-view DROP VIEW round-trips
		// raw.
		if len(s.Tables) != 1 {
			return nil, sqlparse.Unsupported("DROP VIEW of multiple views")
		}
		stmt := sql.DropView(tableFromName(s.Tables[0]))
		if s.IfExists {
			stmt = stmt.IfExists()
		}
		return stmt, nil
	}
	tables := make([]sql.Table, 0, len(s.Tables))
	for _, tn := range s.Tables {
		tables = append(tables, tableFromName(tn))
	}
	stmt := sql.DropTable(tables...)
	if s.IfExists {
		stmt = stmt.IfExists()
	}
	return stmt, nil
}

func convertCreateIndex(s *ast.CreateIndexStmt) (sql.Query, error) {
	switch s.KeyType {
	case ast.IndexKeyTypeNone, ast.IndexKeyTypeUnique:
		// Plain and UNIQUE indexes map; SPATIAL/FULLTEXT have no typed home.
	default:
		return nil, sqlparse.Unsupportedf("%s index", indexKeyTypeName(s.KeyType))
	}
	columns, err := indexColumns(s.IndexPartSpecifications)
	if err != nil {
		return nil, err
	}
	stmt := sql.CreateIndex(s.IndexName, tableFromName(s.Table), columns...)
	if s.KeyType == ast.IndexKeyTypeUnique {
		stmt = stmt.Unique()
	}
	if s.IfNotExists {
		stmt = stmt.IfNotExists()
	}
	return stmt, nil
}

func convertDropIndex(s *ast.DropIndexStmt) (sql.Query, error) {
	if s.IfExists {
		// sqlkit's DropIndex has no IF EXISTS; round-trip raw to keep it.
		return nil, sqlparse.Unsupported("DROP INDEX IF EXISTS")
	}
	return sql.DropIndex(s.IndexName, tableFromName(s.Table)), nil
}

func convertRenameTable(s *ast.RenameTableStmt) (sql.Query, error) {
	// MySQL allows several renames in one statement; sqlkit models a single
	// rename, so multi-pair RENAME TABLE round-trips raw.
	if len(s.TableToTables) != 1 {
		return nil, sqlparse.Unsupported("RENAME TABLE of multiple tables")
	}
	pair := s.TableToTables[0]
	if pair.NewTable.Schema.O != "" && pair.NewTable.Schema.O != pair.OldTable.Schema.O {
		return nil, sqlparse.Unsupported("RENAME TABLE across schemas")
	}
	return sql.RenameTable(tableFromName(pair.OldTable), pair.NewTable.Name.O), nil
}

// indexColumns extracts plain column names from index key parts, rejecting
// prefix lengths and functional (expression) keys that have no typed home.
func indexColumns(parts []*ast.IndexPartSpecification) ([]string, error) {
	cols := make([]string, 0, len(parts))
	for _, part := range parts {
		if part.Expr != nil {
			return nil, sqlparse.Unsupported("functional index key")
		}
		if part.Length > 0 {
			return nil, sqlparse.Unsupported("index prefix length")
		}
		if part.Column == nil {
			return nil, sqlparse.Unsupported("index key without a column")
		}
		cols = append(cols, part.Column.Name.O)
	}
	return cols, nil
}

func indexKeyTypeName(t ast.IndexKeyType) string {
	switch t {
	case ast.IndexKeyTypeSpatial:
		return "SPATIAL"
	case ast.IndexKeyTypeFulltext:
		return "FULLTEXT"
	default:
		return "non-plain"
	}
}

func convertTruncate(s *ast.TruncateTableStmt) (sql.Query, error) {
	return sql.Truncate(tableFromName(s.Table)), nil
}

func convertCreateDatabase(s *ast.CreateDatabaseStmt) (sql.Query, error) {
	stmt := sql.CreateSchema(s.Name.O)
	if s.IfNotExists {
		stmt = stmt.IfNotExists()
	}
	return stmt, nil
}

func convertDropDatabase(s *ast.DropDatabaseStmt) (sql.Query, error) {
	stmt := sql.DropSchema(s.Name.O)
	if s.IfExists {
		stmt = stmt.IfExists()
	}
	return stmt, nil
}

// staticExpr converts a DDL expression (a CHECK predicate or column DEFAULT),
// which binds no values: literals render verbatim rather than as bind parameters.
func staticExpr(node ast.ExprNode) (sql.Expression, error) {
	switch e := node.(type) {
	case ast.ValueExpr:
		return staticConst(e)
	case *ast.ColumnNameExpr:
		return sql.Col(columnRefName(e.Name)), nil
	case *ast.ParenthesesExpr:
		return staticExpr(e.Expr)
	case *ast.FuncCallExpr:
		args := make([]any, 0, len(e.Args))
		for _, a := range e.Args {
			expr, err := staticExpr(a)
			if err != nil {
				return nil, err
			}
			args = append(args, expr)
		}
		return sql.Func(strings.ToUpper(e.FnName.L), args...), nil
	case *ast.BinaryOperationExpr:
		left, err := staticExpr(e.L)
		if err != nil {
			return nil, err
		}
		right, err := staticExpr(e.R)
		if err != nil {
			return nil, err
		}
		op, ok := binaryOperators[e.Op]
		if !ok {
			return nil, sqlparse.Unsupportedf("static operator %s", e.Op)
		}
		return sql.BinaryExpression{Left: left, Operator: op, Right: right}, nil
	default:
		return nil, sqlparse.Unsupportedf("static expression %T", node)
	}
}

// staticConst renders a constant literal as raw SQL text for DDL contexts.
func staticConst(v ast.ValueExpr) (sql.Expression, error) {
	switch x := v.GetValue().(type) {
	case nil:
		return sql.Raw("NULL"), nil
	case int64:
		return sql.Raw(strconv.FormatInt(x, 10)), nil
	case uint64:
		return sql.Raw(strconv.FormatUint(x, 10)), nil
	case float64:
		return sql.Raw(strconv.FormatFloat(x, 'g', -1, 64)), nil
	case string:
		return sql.Raw("'" + escapeStringLiteral(x) + "'"), nil
	default:
		if s, ok := v.GetValue().(interface{ String() string }); ok {
			return sql.Raw(s.String()), nil
		}
		return nil, sqlparse.Unsupportedf("static constant %T", v.GetValue())
	}
}

// stringLiteral reads a string-valued literal (a COMMENT, for example).
func stringLiteral(node ast.ExprNode) (string, error) {
	v, ok := node.(ast.ValueExpr)
	if !ok {
		return "", sqlparse.Unsupportedf("string literal %T", node)
	}
	s, ok := v.GetValue().(string)
	if !ok {
		return "", sqlparse.Unsupported("non-string literal")
	}
	return s, nil
}

func escapeStringLiteral(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}
