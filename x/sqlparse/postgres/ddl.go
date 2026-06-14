package postgres

import (
	"strconv"
	"strings"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/x/sqlparse"
	pg "github.com/pganalyze/pg_query_go/v6"
)

func convertCreateTable(s *pg.CreateStmt) (sql.Query, error) {
	rv := s.GetRelation()
	ct := sql.CreateTable(sql.Table{Schema: rv.GetSchemaname(), Name: rv.GetRelname()})
	if s.GetIfNotExists() {
		ct = ct.IfNotExists()
	}

	var primaryKey []string
	for _, elt := range s.GetTableElts() {
		switch {
		case elt.GetColumnDef() != nil:
			col, isPK, err := convertColumnDef(elt.GetColumnDef())
			if err != nil {
				return nil, err
			}
			ct = ct.Column(col)
			if isPK {
				primaryKey = append(primaryKey, col.Name)
			}
		case elt.GetConstraint() != nil:
			con := elt.GetConstraint()
			switch con.GetContype() {
			case pg.ConstrType_CONSTR_PRIMARY:
				keys, err := constraintKeys(con)
				if err != nil {
					return nil, err
				}
				primaryKey = keys
			case pg.ConstrType_CONSTR_CHECK:
				expr, err := staticExpr(con.GetRawExpr())
				if err != nil {
					return nil, err
				}
				ct = ct.Check(con.GetConname(), expr)
			default:
				return nil, sqlparse.Unsupportedf("table constraint %s", con.GetContype())
			}
		default:
			return nil, sqlparse.Unsupportedf("table element %T", elt.GetNode())
		}
	}
	if len(primaryKey) > 0 {
		ct = ct.PrimaryKey(primaryKey...)
	}
	return ct, nil
}

// convertColumnDef builds a ColumnDef and reports whether an inline PRIMARY KEY
// constraint marks it as the table's primary key.
func convertColumnDef(cd *pg.ColumnDef) (sql.ColumnDef, bool, error) {
	typeName, err := typeNameString(cd.GetTypeName())
	if err != nil {
		return sql.ColumnDef{}, false, err
	}
	def := sql.ColumnDef{Name: cd.GetColname(), Type: sql.RawType(typeName)}

	var isPK bool
	for _, c := range cd.GetConstraints() {
		con := c.GetConstraint()
		if con == nil {
			return sql.ColumnDef{}, false, sqlparse.Unsupportedf("column constraint %T", c.GetNode())
		}
		switch con.GetContype() {
		case pg.ConstrType_CONSTR_NOTNULL:
			def.NotNull = true
		case pg.ConstrType_CONSTR_NULL:
			// Explicit NULL is the default; nothing to record.
		case pg.ConstrType_CONSTR_PRIMARY:
			isPK = true
		case pg.ConstrType_CONSTR_DEFAULT:
			expr, err := staticExpr(con.GetRawExpr())
			if err != nil {
				return sql.ColumnDef{}, false, err
			}
			def.Default = expr
		default:
			return sql.ColumnDef{}, false, sqlparse.Unsupportedf("column constraint %s", con.GetContype())
		}
	}
	return def, isPK, nil
}

func constraintKeys(con *pg.Constraint) ([]string, error) {
	keys := make([]string, 0, len(con.GetKeys()))
	for _, key := range con.GetKeys() {
		if key.GetString_() == nil {
			return nil, sqlparse.Unsupportedf("constraint key %T", key.GetNode())
		}
		keys = append(keys, key.GetString_().GetSval())
	}
	return keys, nil
}

func convertAlterTable(s *pg.AlterTableStmt) (sql.Query, error) {
	rv := s.GetRelation()
	at := sql.AlterTable(sql.Table{Schema: rv.GetSchemaname(), Name: rv.GetRelname()})

	for _, cmdNode := range s.GetCmds() {
		cmd := cmdNode.GetAlterTableCmd()
		if cmd == nil {
			return nil, sqlparse.Unsupportedf("alter command %T", cmdNode.GetNode())
		}
		switch cmd.GetSubtype() {
		case pg.AlterTableType_AT_AddColumn:
			col, _, err := convertColumnDef(cmd.GetDef().GetColumnDef())
			if err != nil {
				return nil, err
			}
			at = at.AddColumn(col)
		case pg.AlterTableType_AT_DropColumn:
			at = at.DropColumn(cmd.GetName())
		case pg.AlterTableType_AT_SetNotNull:
			at = at.SetNotNull(cmd.GetName())
		case pg.AlterTableType_AT_DropNotNull:
			at = at.DropNotNull(cmd.GetName())
		case pg.AlterTableType_AT_ColumnDefault:
			if def := cmd.GetDef(); def != nil {
				expr, err := staticExpr(def)
				if err != nil {
					return nil, err
				}
				at = at.SetDefault(cmd.GetName(), expr)
			} else {
				at = at.DropDefault(cmd.GetName())
			}
		case pg.AlterTableType_AT_AlterColumnType:
			typeName, err := typeNameString(cmd.GetDef().GetColumnDef().GetTypeName())
			if err != nil {
				return nil, err
			}
			at = at.AlterColumnType(cmd.GetName(), sql.RawType(typeName))
		default:
			return nil, sqlparse.Unsupportedf("ALTER TABLE action %s", cmd.GetSubtype())
		}
	}
	return at, nil
}

func convertDrop(s *pg.DropStmt) (sql.Query, error) {
	switch s.GetRemoveType() {
	case pg.ObjectType_OBJECT_TABLE:
		if len(s.GetObjects()) != 1 {
			return nil, sqlparse.Unsupported("DROP TABLE of multiple tables")
		}
		table, err := tableFromNameList(s.GetObjects()[0])
		if err != nil {
			return nil, err
		}
		stmt := sql.DropTable(table)
		if s.GetMissingOk() {
			stmt = stmt.IfExists()
		}
		return stmt, nil
	case pg.ObjectType_OBJECT_SCHEMA:
		if len(s.GetObjects()) != 1 {
			return nil, sqlparse.Unsupported("DROP SCHEMA of multiple schemas")
		}
		nameNode := s.GetObjects()[0]
		if nameNode.GetString_() == nil {
			return nil, sqlparse.Unsupportedf("schema name %T", nameNode.GetNode())
		}
		stmt := sql.DropSchema(nameNode.GetString_().GetSval())
		if s.GetMissingOk() {
			stmt = stmt.IfExists()
		}
		if s.GetBehavior() == pg.DropBehavior_DROP_CASCADE {
			stmt = stmt.Cascade()
		}
		return stmt, nil
	default:
		return nil, sqlparse.Unsupportedf("DROP of %s", s.GetRemoveType())
	}
}

func convertCreateSchema(s *pg.CreateSchemaStmt) (sql.Query, error) {
	stmt := sql.CreateSchema(s.GetSchemaname())
	if s.GetIfNotExists() {
		stmt = stmt.IfNotExists()
	}
	return stmt, nil
}

// tableFromNameList builds a Table from a DROP object, a List of name parts
// ("schema", "table") or just ("table").
func tableFromNameList(node *pg.Node) (sql.Table, error) {
	list := node.GetList()
	if list == nil {
		return sql.Table{}, sqlparse.Unsupportedf("table name %T", node.GetNode())
	}
	parts := make([]string, 0, len(list.GetItems()))
	for _, item := range list.GetItems() {
		if item.GetString_() == nil {
			return sql.Table{}, sqlparse.Unsupportedf("table name part %T", item.GetNode())
		}
		parts = append(parts, item.GetString_().GetSval())
	}
	switch len(parts) {
	case 1:
		return sql.Table{Name: parts[0]}, nil
	case 2:
		return sql.Table{Schema: parts[0], Name: parts[1]}, nil
	default:
		return sql.Table{}, sqlparse.Unsupportedf("qualified table name %q", strings.Join(parts, "."))
	}
}

// staticExpr converts a DDL expression (a CHECK predicate or column DEFAULT),
// which must bind no values: literals render verbatim rather than as bind
// parameters.
func staticExpr(node *pg.Node) (sql.Expression, error) {
	switch {
	case node.GetAConst() != nil:
		return staticConst(node.GetAConst())
	case node.GetColumnRef() != nil:
		return convertColumnRef(node.GetColumnRef())
	case node.GetAExpr() != nil:
		e := node.GetAExpr()
		if e.GetKind() != pg.A_Expr_Kind_AEXPR_OP || e.GetLexpr() == nil {
			return nil, sqlparse.Unsupportedf("static operator expression kind %s", e.GetKind())
		}
		op, err := operatorName(e.GetName())
		if err != nil {
			return nil, err
		}
		left, err := staticExpr(e.GetLexpr())
		if err != nil {
			return nil, err
		}
		right, err := staticExpr(e.GetRexpr())
		if err != nil {
			return nil, err
		}
		return sql.BinaryExpression{Left: left, Operator: op, Right: right}, nil
	case node.GetBoolExpr() != nil:
		e := node.GetBoolExpr()
		operands := make([]sql.Expression, 0, len(e.GetArgs()))
		for _, arg := range e.GetArgs() {
			expr, err := staticExpr(arg)
			if err != nil {
				return nil, err
			}
			operands = append(operands, expr)
		}
		switch e.GetBoolop() {
		case pg.BoolExprType_AND_EXPR:
			return sql.And(operands...), nil
		case pg.BoolExprType_OR_EXPR:
			return sql.Or(operands...), nil
		case pg.BoolExprType_NOT_EXPR:
			return sql.Not(operands[0]), nil
		}
		return nil, sqlparse.Unsupportedf("static boolean operator %s", e.GetBoolop())
	case node.GetFuncCall() != nil:
		fc := node.GetFuncCall()
		name, err := funcName(fc.GetFuncname())
		if err != nil {
			return nil, err
		}
		args := make([]any, 0, len(fc.GetArgs()))
		for _, a := range fc.GetArgs() {
			expr, err := staticExpr(a)
			if err != nil {
				return nil, err
			}
			args = append(args, expr)
		}
		return sql.Func(name, args...), nil
	case node.GetTypeCast() != nil:
		tc := node.GetTypeCast()
		inner, err := staticExpr(tc.GetArg())
		if err != nil {
			return nil, err
		}
		typeName, err := typeNameString(tc.GetTypeName())
		if err != nil {
			return nil, err
		}
		return sql.Cast(inner, typeName), nil
	default:
		return nil, sqlparse.Unsupportedf("static expression node %T", node.GetNode())
	}
}

// staticConst renders a constant literal as raw SQL text for DDL contexts.
func staticConst(c *pg.A_Const) (sql.Expression, error) {
	switch {
	case c.GetIsnull():
		return sql.Raw("NULL"), nil
	case c.GetIval() != nil:
		return sql.Raw(strconv.Itoa(int(c.GetIval().GetIval()))), nil
	case c.GetFval() != nil:
		return sql.Raw(c.GetFval().GetFval()), nil
	case c.GetSval() != nil:
		return sql.Raw("'" + strings.ReplaceAll(c.GetSval().GetSval(), "'", "''") + "'"), nil
	case c.GetBoolval() != nil:
		if c.GetBoolval().GetBoolval() {
			return sql.Raw("true"), nil
		}
		return sql.Raw("false"), nil
	default:
		return nil, sqlparse.Unsupported("static constant literal")
	}
}
