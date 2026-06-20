package postgres

import (
	"strconv"
	"strings"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse"
	pg "github.com/pganalyze/pg_query_go/v6"
)

// convertCreateIndex converts CREATE [UNIQUE] INDEX over a plain column list.
// Forms the CreateIndex builder cannot model (expression/partial/covering
// indexes, a non-btree access method, opclasses, orderings, options) report
// Unsupported so Parse falls back to a raw passthrough.
func convertCreateIndex(s *pg.IndexStmt) (sql.Query, error) {
	if s.GetIdxname() == "" {
		return nil, sqlparse.Unsupported("CREATE INDEX without a name")
	}
	if s.GetConcurrent() {
		return nil, sqlparse.Unsupported("CREATE INDEX CONCURRENTLY")
	}
	if am := s.GetAccessMethod(); am != "" && am != "btree" {
		return nil, sqlparse.Unsupportedf("CREATE INDEX USING %s", am)
	}
	if s.GetWhereClause() != nil {
		return nil, sqlparse.Unsupported("partial index (WHERE)")
	}
	if len(s.GetIndexIncludingParams()) > 0 {
		return nil, sqlparse.Unsupported("INCLUDE columns")
	}
	if len(s.GetOptions()) > 0 || s.GetTableSpace() != "" || s.GetNullsNotDistinct() {
		return nil, sqlparse.Unsupported("index storage options")
	}
	columns := make([]string, 0, len(s.GetIndexParams()))
	for _, param := range s.GetIndexParams() {
		ie := param.GetIndexElem()
		if ie == nil || ie.GetName() == "" || ie.GetExpr() != nil ||
			len(ie.GetOpclass()) > 0 || len(ie.GetCollation()) > 0 ||
			ie.GetOrdering() != pg.SortByDir_SORTBY_DEFAULT ||
			ie.GetNullsOrdering() != pg.SortByNulls_SORTBY_NULLS_DEFAULT {
			return nil, sqlparse.Unsupported("expression or qualified index column")
		}
		columns = append(columns, ie.GetName())
	}
	stmt := sql.CreateIndex(s.GetIdxname(), tableFromRangeVar(s.GetRelation()), columns...)
	if s.GetUnique() {
		stmt = stmt.Unique()
	}
	if s.GetIfNotExists() {
		stmt = stmt.IfNotExists()
	}
	return stmt, nil
}

// convertCreateView converts CREATE [OR REPLACE] VIEW whose body is a SELECT.
// The view compiler requires a static body, so a body whose literals would bind
// values reports Unsupported and round-trips raw instead.
func convertCreateView(s *pg.ViewStmt) (sql.Query, error) {
	if s.GetWithCheckOption() != pg.ViewCheckOption_NO_CHECK_OPTION {
		return nil, sqlparse.Unsupported("WITH CHECK OPTION")
	}
	if len(s.GetOptions()) > 0 {
		return nil, sqlparse.Unsupported("view WITH options")
	}
	sel := s.GetQuery().GetSelectStmt()
	if sel == nil {
		return nil, sqlparse.Unsupportedf("view body %T", s.GetQuery().GetNode())
	}
	query, err := convertSelect(sel)
	if err != nil {
		return nil, err
	}
	if _, args, err := query.ToSQL(sql.PostgreSQLDialect{}); err != nil {
		return nil, err
	} else if len(args) > 0 {
		return nil, sqlparse.Unsupported("view body with bound literals")
	}
	stmt := sql.CreateView(tableFromRangeVar(s.GetView()), query)
	if s.GetReplace() {
		stmt = stmt.OrReplace()
	}
	if aliases := s.GetAliases(); len(aliases) > 0 {
		columns, err := stringList(aliases)
		if err != nil {
			return nil, err
		}
		stmt = stmt.Columns(columns...)
	}
	return stmt, nil
}

// convertComment converts COMMENT ON TABLE / COLUMN. An empty comment (IS NULL)
// is not modeled — the standalone-comment compiler emits nothing for it — so it
// round-trips raw.
func convertComment(s *pg.CommentStmt) (sql.Query, error) {
	if s.GetComment() == "" {
		return nil, sqlparse.Unsupported("COMMENT ... IS NULL")
	}
	switch s.GetObjtype() {
	case pg.ObjectType_OBJECT_TABLE:
		table, err := tableFromNameList(s.GetObject())
		if err != nil {
			return nil, err
		}
		return sql.CommentOnTable(table, s.GetComment()), nil
	case pg.ObjectType_OBJECT_COLUMN:
		table, column, err := tableColumnFromNameList(s.GetObject())
		if err != nil {
			return nil, err
		}
		return sql.CommentOnColumn(table, column, s.GetComment()), nil
	default:
		return nil, sqlparse.Unsupportedf("COMMENT ON %s", s.GetObjtype())
	}
}

// convertCreateExtension converts CREATE EXTENSION [IF NOT EXISTS] [WITH SCHEMA].
// Other options (VERSION, CASCADE) are not modeled and round-trip raw.
func convertCreateExtension(s *pg.CreateExtensionStmt) (sql.Query, error) {
	stmt := sql.CreateExtension(s.GetExtname())
	if s.GetIfNotExists() {
		stmt = stmt.IfNotExists()
	}
	for _, opt := range s.GetOptions() {
		de := opt.GetDefElem()
		if de == nil {
			return nil, sqlparse.Unsupportedf("extension option %T", opt.GetNode())
		}
		if de.GetDefname() != "schema" {
			return nil, sqlparse.Unsupportedf("extension option %s", de.GetDefname())
		}
		schema, err := defElemString(de)
		if err != nil {
			return nil, err
		}
		stmt = stmt.Schema(schema)
	}
	return stmt, nil
}

// convertCreateEnum converts CREATE TYPE name AS ENUM (...).
func convertCreateEnum(s *pg.CreateEnumStmt) (sql.Query, error) {
	name, err := dottedName(s.GetTypeName())
	if err != nil {
		return nil, err
	}
	values, err := stringList(s.GetVals())
	if err != nil {
		return nil, err
	}
	return sql.CreateEnum(name, values...), nil
}

// convertRename converts the table and column rename forms of RenameStmt; other
// rename targets round-trip raw.
func convertRename(s *pg.RenameStmt) (sql.Query, error) {
	switch s.GetRenameType() {
	case pg.ObjectType_OBJECT_TABLE:
		return sql.RenameTable(tableFromRangeVar(s.GetRelation()), s.GetNewname()), nil
	case pg.ObjectType_OBJECT_COLUMN:
		return sql.RenameColumn(tableFromRangeVar(s.GetRelation()), s.GetSubname(), s.GetNewname()), nil
	default:
		return nil, sqlparse.Unsupportedf("RENAME of %s", s.GetRenameType())
	}
}

// dottedName joins the String parts of a name list into a dotted identifier.
func dottedName(nodes []*pg.Node) (string, error) {
	parts, err := stringList(nodes)
	if err != nil {
		return "", err
	}
	return strings.Join(parts, "."), nil
}

// defElemString reads a DefElem's argument as a string.
func defElemString(de *pg.DefElem) (string, error) {
	arg := de.GetArg()
	if arg == nil || arg.GetString_() == nil {
		return "", sqlparse.Unsupportedf("option %s value", de.GetDefname())
	}
	return arg.GetString_().GetSval(), nil
}

// dropObjectName renders a DROP object as a dotted identifier, accepting a bare
// String, a name List, or a TypeName.
func dropObjectName(node *pg.Node) (string, error) {
	switch {
	case node.GetString_() != nil:
		return node.GetString_().GetSval(), nil
	case node.GetList() != nil:
		return dottedName(node.GetList().GetItems())
	case node.GetTypeName() != nil:
		return dottedName(node.GetTypeName().GetNames())
	default:
		return "", sqlparse.Unsupportedf("drop object %T", node.GetNode())
	}
}

func dropObjectNames(nodes []*pg.Node) ([]string, error) {
	names := make([]string, 0, len(nodes))
	for _, node := range nodes {
		name, err := dropObjectName(node)
		if err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, nil
}

// convertCreateSequence converts CREATE SEQUENCE with the common options. The
// AS type and RESTART options are not modeled and round-trip raw.
func convertCreateSequence(s *pg.CreateSeqStmt) (sql.Query, error) {
	stmt := sql.CreateSequence(tableFromRangeVar(s.GetSequence()))
	if s.GetIfNotExists() {
		stmt = stmt.IfNotExists()
	}
	for _, opt := range s.GetOptions() {
		de := opt.GetDefElem()
		if de == nil {
			return nil, sqlparse.Unsupportedf("sequence option %T", opt.GetNode())
		}
		switch de.GetDefname() {
		case "increment":
			v, err := defElemInt(de)
			if err != nil {
				return nil, err
			}
			stmt = stmt.Increment(v)
		case "start":
			v, err := defElemInt(de)
			if err != nil {
				return nil, err
			}
			stmt = stmt.Start(v)
		case "cache":
			v, err := defElemInt(de)
			if err != nil {
				return nil, err
			}
			stmt = stmt.Cache(v)
		case "minvalue":
			if de.GetArg() == nil {
				stmt = stmt.NoMinValue()
			} else {
				v, err := defElemInt(de)
				if err != nil {
					return nil, err
				}
				stmt = stmt.MinValue(v)
			}
		case "maxvalue":
			if de.GetArg() == nil {
				stmt = stmt.NoMaxValue()
			} else {
				v, err := defElemInt(de)
				if err != nil {
					return nil, err
				}
				stmt = stmt.MaxValue(v)
			}
		case "cycle":
			if de.GetArg().GetBoolean().GetBoolval() {
				stmt = stmt.Cycle()
			} else {
				stmt = stmt.NoCycle()
			}
		case "owned_by":
			name, err := ownedByName(de.GetArg())
			if err != nil {
				return nil, err
			}
			stmt = stmt.OwnedBy(name)
		default:
			return nil, sqlparse.Unsupportedf("sequence option %s", de.GetDefname())
		}
	}
	return stmt, nil
}

// defElemInt reads a DefElem integer argument, accepting the Integer node and
// the Float node PostgreSQL uses for values too wide for int32.
func defElemInt(de *pg.DefElem) (int64, error) {
	arg := de.GetArg()
	switch {
	case arg.GetInteger() != nil:
		return int64(arg.GetInteger().GetIval()), nil
	case arg.GetFloat() != nil:
		return strconv.ParseInt(arg.GetFloat().GetFval(), 10, 64)
	default:
		return 0, sqlparse.Unsupportedf("sequence option %s value", de.GetDefname())
	}
}

// ownedByName renders an OWNED BY argument: "table.column" parts, or "NONE".
func ownedByName(arg *pg.Node) (string, error) {
	list := arg.GetList()
	if list == nil {
		return "", sqlparse.Unsupported("OWNED BY value")
	}
	parts, err := stringList(list.GetItems())
	if err != nil {
		return "", err
	}
	if len(parts) == 1 && strings.EqualFold(parts[0], "none") {
		return "NONE", nil
	}
	return strings.Join(parts, "."), nil
}

// convertCreateTableAs converts CREATE MATERIALIZED VIEW (a CreateTableAsStmt
// with a materialized-view object type). Plain CREATE TABLE AS / SELECT INTO and
// bodies that would bind values are not modeled and round-trip raw.
func convertCreateTableAs(s *pg.CreateTableAsStmt) (sql.Query, error) {
	if s.GetObjtype() != pg.ObjectType_OBJECT_MATVIEW {
		return nil, sqlparse.Unsupported("CREATE TABLE AS / SELECT INTO")
	}
	into := s.GetInto()
	if into == nil || into.GetRel() == nil {
		return nil, sqlparse.Unsupported("materialized view without a target")
	}
	sel := s.GetQuery().GetSelectStmt()
	if sel == nil {
		return nil, sqlparse.Unsupportedf("materialized view body %T", s.GetQuery().GetNode())
	}
	query, err := convertSelect(sel)
	if err != nil {
		return nil, err
	}
	if _, args, err := query.ToSQL(sql.PostgreSQLDialect{}); err != nil {
		return nil, err
	} else if len(args) > 0 {
		return nil, sqlparse.Unsupported("materialized view body with bound literals")
	}
	stmt := sql.CreateMaterializedView(tableFromRangeVar(into.GetRel()), query)
	if s.GetIfNotExists() {
		stmt = stmt.IfNotExists()
	}
	if cols := into.GetColNames(); len(cols) > 0 {
		names, err := stringList(cols)
		if err != nil {
			return nil, err
		}
		stmt = stmt.Columns(names...)
	}
	if into.GetSkipData() {
		stmt = stmt.WithNoData()
	}
	return stmt, nil
}

// convertRefreshMatView converts REFRESH MATERIALIZED VIEW.
func convertRefreshMatView(s *pg.RefreshMatViewStmt) (sql.Query, error) {
	stmt := sql.RefreshMaterializedView(tableFromRangeVar(s.GetRelation()))
	if s.GetConcurrent() {
		stmt = stmt.Concurrently()
	}
	if s.GetSkipData() {
		stmt = stmt.WithNoData()
	}
	return stmt, nil
}

// convertAlterEnum converts ALTER TYPE ... ADD VALUE / RENAME VALUE.
func convertAlterEnum(s *pg.AlterEnumStmt) (sql.Query, error) {
	name, err := dottedName(s.GetTypeName())
	if err != nil {
		return nil, err
	}
	// RENAME VALUE carries both an old and a new value; ADD VALUE only a new one.
	if s.GetOldVal() != "" {
		return sql.AlterTypeRenameValue(name, s.GetOldVal(), s.GetNewVal()), nil
	}
	stmt := sql.AlterTypeAddValue(name, s.GetNewVal())
	if s.GetSkipIfNewValExists() {
		stmt = stmt.IfNotExists()
	}
	if neighbor := s.GetNewValNeighbor(); neighbor != "" {
		if s.GetNewValIsAfter() {
			stmt = stmt.After(neighbor)
		} else {
			stmt = stmt.Before(neighbor)
		}
	}
	return stmt, nil
}

// convertTransaction converts the transaction-control statements. BEGIN/START
// with options (ISOLATION LEVEL, ...) and the two-phase-commit forms are not
// modeled and round-trip raw.
func convertTransaction(s *pg.TransactionStmt) (sql.Query, error) {
	switch s.GetKind() {
	case pg.TransactionStmtKind_TRANS_STMT_BEGIN, pg.TransactionStmtKind_TRANS_STMT_START:
		if len(s.GetOptions()) > 0 {
			return nil, sqlparse.Unsupported("BEGIN with transaction modes")
		}
		return sql.Begin(), nil
	case pg.TransactionStmtKind_TRANS_STMT_COMMIT:
		if s.GetChain() {
			return nil, sqlparse.Unsupported("COMMIT AND CHAIN")
		}
		return sql.Commit(), nil
	case pg.TransactionStmtKind_TRANS_STMT_ROLLBACK:
		if s.GetChain() {
			return nil, sqlparse.Unsupported("ROLLBACK AND CHAIN")
		}
		return sql.Rollback(), nil
	case pg.TransactionStmtKind_TRANS_STMT_SAVEPOINT:
		return sql.Savepoint(s.GetSavepointName()), nil
	case pg.TransactionStmtKind_TRANS_STMT_RELEASE:
		return sql.ReleaseSavepoint(s.GetSavepointName()), nil
	case pg.TransactionStmtKind_TRANS_STMT_ROLLBACK_TO:
		return sql.RollbackTo(s.GetSavepointName()), nil
	default:
		return nil, sqlparse.Unsupportedf("transaction statement %s", s.GetKind())
	}
}

// tableColumnFromNameList splits a column object name list ("table","column" or
// "schema","table","column") into the owning table and the column name.
func tableColumnFromNameList(node *pg.Node) (sql.Table, string, error) {
	list := node.GetList()
	if list == nil {
		return sql.Table{}, "", sqlparse.Unsupportedf("column name %T", node.GetNode())
	}
	parts, err := stringList(list.GetItems())
	if err != nil {
		return sql.Table{}, "", err
	}
	switch len(parts) {
	case 2:
		return sql.Table{Name: sql.NewIdent(parts[0])}, parts[1], nil
	case 3:
		return sql.Table{Schema: sql.NewIdent(parts[0]), Name: sql.NewIdent(parts[1])}, parts[2], nil
	default:
		return sql.Table{}, "", sqlparse.Unsupported("qualified column name")
	}
}
