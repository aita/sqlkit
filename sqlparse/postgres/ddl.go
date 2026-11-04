package postgres

import (
	"strconv"
	"strings"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse"
	pg "github.com/pganalyze/pg_query_go/v6"
)

func convertCreateTable(s *pg.CreateStmt) (sql.Query, error) {
	// A handful of CREATE TABLE attributes have no sql.CreateTableBuilder home:
	// INHERITS (an inheritance relationship — dropping it silently loses the
	// parent's constraints and the parent-query visibility of this table's rows),
	// PARTITION BY (silently creates a plain heap table instead of a partitioned
	// one), TEMP/UNLOGGED persistence (silently becomes a permanent, logged
	// table), TABLESPACE/WITH storage params/USING access method (placement-only,
	// but still a construct sqlkit cannot re-render), and ON COMMIT (changes
	// whether/how a temp table's data survives a transaction boundary). Detect
	// each and fall back to raw rather than silently drop it. OF <type> and
	// PARTITION OF have no table-element list to convert (s.GetTableElts() would
	// be empty), which built an unusable zero-column CreateTableStmt whose ToSQL
	// hard-errors; reject them here too so the failure is a raw fallback at parse
	// time, not a ToSQL error on an otherwise-parseable statement.
	if len(s.GetInhRelations()) > 0 {
		return nil, sqlparse.Unsupported("CREATE TABLE ... INHERITS")
	}
	if s.GetPartspec() != nil {
		return nil, sqlparse.Unsupported("CREATE TABLE ... PARTITION BY")
	}
	if s.GetOfTypename() != nil {
		return nil, sqlparse.Unsupported("CREATE TABLE ... OF <type>")
	}
	if s.GetPartbound() != nil {
		return nil, sqlparse.Unsupported("CREATE TABLE ... PARTITION OF")
	}
	if s.GetTablespacename() != "" {
		return nil, sqlparse.Unsupported("CREATE TABLE ... TABLESPACE")
	}
	if len(s.GetOptions()) > 0 {
		return nil, sqlparse.Unsupported("CREATE TABLE ... WITH (storage parameters)")
	}
	if s.GetOncommit() != pg.OnCommitAction_ONCOMMIT_NOOP {
		return nil, sqlparse.Unsupported("CREATE TABLE ... ON COMMIT")
	}
	if s.GetAccessMethod() != "" {
		return nil, sqlparse.Unsupported("CREATE TABLE ... USING <access method>")
	}

	rv := s.GetRelation()
	ct := sql.CreateTable(sql.Table{Schema: sql.NewIdent(rv.GetSchemaname()), Name: sql.NewIdent(rv.GetRelname())})
	switch rv.GetRelpersistence() {
	case "t":
		ct = ct.Temporary()
	case "u":
		// UNLOGGED has no sql.CreateTableBuilder equivalent; dropping it silently
		// changes the table's durability/WAL guarantee.
		return nil, sqlparse.Unsupported("CREATE UNLOGGED TABLE")
	}
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
				primaryKey = append(primaryKey, col.Name.String())
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
			case pg.ConstrType_CONSTR_UNIQUE:
				keys, err := constraintKeys(con)
				if err != nil {
					return nil, err
				}
				ct = ct.Unique(con.GetConname(), keys...)
			case pg.ConstrType_CONSTR_FOREIGN:
				fk, err := convertForeignKey(con)
				if err != nil {
					return nil, err
				}
				ct = ct.ForeignKey(fk)
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
	def := sql.ColumnDef{Name: sql.NewIdentAt(cd.GetColname(), int(cd.GetLocation())), Type: sql.RawType(typeName)}

	// A column COLLATE clause has no typed home here that renders correctly: the
	// DDL compiler emits the collation name unquoted, but PostgreSQL collation
	// names ("C", "en_US") are case-sensitive and must stay quoted. Rather than
	// drop the collation or render it wrong, fall back to raw so it is preserved.
	if cd.GetCollClause() != nil {
		return sql.ColumnDef{}, false, sqlparse.Unsupported("column COLLATE clause")
	}

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
		case pg.ConstrType_CONSTR_UNIQUE:
			def.Unique = true
		case pg.ConstrType_CONSTR_DEFAULT:
			expr, err := staticExpr(con.GetRawExpr())
			if err != nil {
				return sql.ColumnDef{}, false, err
			}
			def.Default = expr
		case pg.ConstrType_CONSTR_FOREIGN:
			ref, err := convertColumnReference(con)
			if err != nil {
				return sql.ColumnDef{}, false, err
			}
			def.References = ref
		default:
			return sql.ColumnDef{}, false, sqlparse.Unsupportedf("column constraint %s", con.GetContype())
		}
	}
	return def, isPK, nil
}

func constraintKeys(con *pg.Constraint) ([]string, error) {
	if err := rejectDeferrable(con); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(con.GetKeys()))
	for _, key := range con.GetKeys() {
		if key.GetString_() == nil {
			return nil, sqlparse.Unsupportedf("constraint key %T", key.GetNode())
		}
		keys = append(keys, key.GetString_().GetSval())
	}
	return keys, nil
}

// rejectDeferrable errors when con is DEFERRABLE (with or without INITIALLY
// DEFERRED). None of sql.CreateTableBuilder's constraint methods has a
// deferrable concept, so dropping it would silently change when the constraint
// is checked — immediately (the default) versus at transaction end — which can
// change whether a transaction that temporarily violates it (e.g. a
// circular-reference update, or a batch of unique-key swaps) succeeds or fails.
func rejectDeferrable(con *pg.Constraint) error {
	if con.GetDeferrable() {
		return sqlparse.Unsupported("a DEFERRABLE constraint")
	}
	return nil
}

func convertAlterTable(s *pg.AlterTableStmt) (sql.Query, error) {
	rv := s.GetRelation()
	// ALTER TABLE IF EXISTS: sql.AlterTableBuilder has no IF EXISTS concept;
	// dropping it changes whether the statement errors on a missing table.
	if s.GetMissingOk() {
		return nil, sqlparse.Unsupported("ALTER TABLE IF EXISTS")
	}
	// ALTER TABLE ONLY excludes inheritance/partition children; dropping it
	// silently widens the alteration to every descendant table (the same class
	// as the already-fixed FROM ONLY / TRUNCATE ONLY).
	if !rv.GetInh() {
		return nil, sqlparse.Unsupported("ALTER TABLE ONLY (inheritance exclusion)")
	}
	at := sql.AlterTable(sql.Table{Schema: sql.NewIdent(rv.GetSchemaname()), Name: sql.NewIdent(rv.GetRelname())})

	for _, cmdNode := range s.GetCmds() {
		cmd := cmdNode.GetAlterTableCmd()
		if cmd == nil {
			return nil, sqlparse.Unsupportedf("alter command %T", cmdNode.GetNode())
		}
		// A per-command IF [NOT] EXISTS (ADD COLUMN IF NOT EXISTS / DROP COLUMN IF
		// EXISTS) has no builder concept either; same reasoning as the statement-level
		// IF EXISTS above.
		if cmd.GetMissingOk() {
			return nil, sqlparse.Unsupported("ALTER TABLE ... IF [NOT] EXISTS (per-command)")
		}
		switch cmd.GetSubtype() {
		case pg.AlterTableType_AT_AddColumn:
			col, _, err := convertColumnDef(cmd.GetDef().GetColumnDef())
			if err != nil {
				return nil, err
			}
			at = at.AddColumn(col)
		case pg.AlterTableType_AT_DropColumn:
			// DROP COLUMN's behavior (CASCADE vs the default RESTRICT) changes whether
			// dependent objects also drop or the statement errors; sql.AlterTableBuilder
			// has no CASCADE concept for DropColumn.
			if cmd.GetBehavior() == pg.DropBehavior_DROP_CASCADE {
				return nil, sqlparse.Unsupported("DROP COLUMN ... CASCADE")
			}
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
			colDef := cmd.GetDef().GetColumnDef()
			// USING <expr> supplies the explicit conversion Postgres runs on the
			// existing data; without it sqlkit would silently drop the user's chosen
			// conversion and let Postgres apply its own default cast, which can
			// convert data differently than intended or fail where the explicit USING
			// would have succeeded. COLLATE changes the column's sort/comparison
			// collation. Neither has a home on sql.AlterColumnType.
			if colDef.GetRawDefault() != nil {
				return nil, sqlparse.Unsupported("ALTER COLUMN ... TYPE ... USING")
			}
			if colDef.GetCollClause() != nil {
				return nil, sqlparse.Unsupported("ALTER COLUMN ... TYPE ... COLLATE")
			}
			typeName, err := typeNameString(colDef.GetTypeName())
			if err != nil {
				return nil, err
			}
			at = at.AlterColumnType(cmd.GetName(), sql.RawType(typeName))
		case pg.AlterTableType_AT_AddConstraint:
			var err error
			if at, err = applyAddConstraint(at, cmd.GetDef().GetConstraint()); err != nil {
				return nil, err
			}
		case pg.AlterTableType_AT_DropConstraint:
			at = at.DropConstraint(cmd.GetName())
		default:
			return nil, sqlparse.Unsupportedf("ALTER TABLE action %s", cmd.GetSubtype())
		}
	}
	return at, nil
}

func convertDrop(s *pg.DropStmt) (sql.Query, error) {
	switch s.GetRemoveType() {
	case pg.ObjectType_OBJECT_TABLE:
		tables := make([]sql.Table, 0, len(s.GetObjects()))
		for _, obj := range s.GetObjects() {
			table, err := tableFromNameList(obj)
			if err != nil {
				return nil, err
			}
			tables = append(tables, table)
		}
		stmt := sql.DropTable(tables...)
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
	case pg.ObjectType_OBJECT_VIEW:
		if len(s.GetObjects()) != 1 {
			return nil, sqlparse.Unsupported("DROP VIEW of multiple views")
		}
		table, err := tableFromNameList(s.GetObjects()[0])
		if err != nil {
			return nil, err
		}
		stmt := sql.DropView(table)
		if s.GetMissingOk() {
			stmt = stmt.IfExists()
		}
		if s.GetBehavior() == pg.DropBehavior_DROP_CASCADE {
			stmt = stmt.Cascade()
		}
		return stmt, nil
	case pg.ObjectType_OBJECT_INDEX:
		// The DropIndex builder always renders IF EXISTS on PostgreSQL, so only the
		// IF EXISTS form round-trips faithfully; a plain DROP INDEX stays raw.
		if !s.GetMissingOk() {
			return nil, sqlparse.Unsupported("DROP INDEX without IF EXISTS")
		}
		if s.GetConcurrent() || s.GetBehavior() == pg.DropBehavior_DROP_CASCADE {
			return nil, sqlparse.Unsupported("DROP INDEX CONCURRENTLY/CASCADE")
		}
		if len(s.GetObjects()) != 1 {
			return nil, sqlparse.Unsupported("DROP INDEX of multiple indexes")
		}
		index, err := tableFromNameList(s.GetObjects()[0])
		if err != nil {
			return nil, err
		}
		return sql.DropIndex(index.Name.String(), sql.Table{Schema: index.Schema}), nil
	case pg.ObjectType_OBJECT_TYPE:
		names, err := dropObjectNames(s.GetObjects())
		if err != nil {
			return nil, err
		}
		stmt := sql.DropType(names...)
		if s.GetMissingOk() {
			stmt = stmt.IfExists()
		}
		if s.GetBehavior() == pg.DropBehavior_DROP_CASCADE {
			stmt = stmt.Cascade()
		}
		return stmt, nil
	case pg.ObjectType_OBJECT_EXTENSION:
		names, err := dropObjectNames(s.GetObjects())
		if err != nil {
			return nil, err
		}
		stmt := sql.DropExtension(names...)
		if s.GetMissingOk() {
			stmt = stmt.IfExists()
		}
		if s.GetBehavior() == pg.DropBehavior_DROP_CASCADE {
			stmt = stmt.Cascade()
		}
		return stmt, nil
	case pg.ObjectType_OBJECT_MATVIEW:
		if len(s.GetObjects()) != 1 {
			return nil, sqlparse.Unsupported("DROP MATERIALIZED VIEW of multiple views")
		}
		table, err := tableFromNameList(s.GetObjects()[0])
		if err != nil {
			return nil, err
		}
		stmt := sql.DropMaterializedView(table)
		if s.GetMissingOk() {
			stmt = stmt.IfExists()
		}
		if s.GetBehavior() == pg.DropBehavior_DROP_CASCADE {
			stmt = stmt.Cascade()
		}
		return stmt, nil
	case pg.ObjectType_OBJECT_SEQUENCE:
		names, err := dropObjectNames(s.GetObjects())
		if err != nil {
			return nil, err
		}
		stmt := sql.DropSequence(names...)
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

func convertTruncate(s *pg.TruncateStmt) (sql.Query, error) {
	tables := make([]sql.Table, 0, len(s.GetRelations()))
	for _, rel := range s.GetRelations() {
		rv := rel.GetRangeVar()
		if rv == nil {
			return nil, sqlparse.Unsupportedf("TRUNCATE relation %T", rel.GetNode())
		}
		// TRUNCATE ONLY excludes inheritance/partition children; dropping it
		// silently widens the truncate to every descendant table (the same class
		// as the already-fixed FROM ONLY on SELECT).
		if !rv.GetInh() {
			return nil, sqlparse.Unsupported("TRUNCATE ONLY (inheritance exclusion)")
		}
		tables = append(tables, sql.Table{Schema: sql.NewIdent(rv.GetSchemaname()), Name: sql.NewIdent(rv.GetRelname())})
	}
	stmt := sql.Truncate(tables...)
	if s.GetRestartSeqs() {
		stmt = stmt.RestartIdentity()
	}
	if s.GetBehavior() == pg.DropBehavior_DROP_CASCADE {
		stmt = stmt.Cascade()
	}
	return stmt, nil
}

// applyAddConstraint maps an ALTER TABLE ADD CONSTRAINT for UNIQUE and FOREIGN
// KEY constraints.
func applyAddConstraint(at sql.AlterTableBuilder, con *pg.Constraint) (sql.AlterTableBuilder, error) {
	if con == nil {
		return at, sqlparse.Unsupported("empty ADD CONSTRAINT")
	}
	// NOT VALID skips validating existing rows against the new constraint;
	// dropping it makes Postgres validate (and potentially fail or take a long
	// table lock) on data the NOT VALID was explicitly used to avoid checking.
	// sql.AlterTableBuilder's Add* constraint methods have no NOT VALID concept.
	if con.GetSkipValidation() {
		return at, sqlparse.Unsupported("ADD CONSTRAINT ... NOT VALID")
	}
	switch con.GetContype() {
	case pg.ConstrType_CONSTR_UNIQUE:
		keys, err := constraintKeys(con)
		if err != nil {
			return at, err
		}
		return at.AddUnique(con.GetConname(), keys...), nil
	case pg.ConstrType_CONSTR_FOREIGN:
		fk, err := convertForeignKey(con)
		if err != nil {
			return at, err
		}
		return at.AddForeignKey(fk), nil
	case pg.ConstrType_CONSTR_CHECK:
		expr, err := staticExpr(con.GetRawExpr())
		if err != nil {
			return at, err
		}
		return at.AddCheck(con.GetConname(), expr), nil
	default:
		return at, sqlparse.Unsupportedf("ADD CONSTRAINT %s", con.GetContype())
	}
}

// convertColumnReference builds a column-level REFERENCES from an inline foreign
// key constraint.
func convertColumnReference(con *pg.Constraint) (*sql.ColumnReference, error) {
	pk := con.GetPktable()
	if pk == nil {
		return nil, sqlparse.Unsupported("foreign key without referenced table")
	}
	if err := rejectFKMatchAndDeferrable(con); err != nil {
		return nil, err
	}
	ref := &sql.ColumnReference{
		Schema:   sql.NewIdent(pk.GetSchemaname()),
		Table:    sql.NewIdentAt(pk.GetRelname(), int(pk.GetLocation())),
		OnDelete: fkAction(con.GetFkDelAction()),
		OnUpdate: fkAction(con.GetFkUpdAction()),
	}
	cols, err := stringList(con.GetPkAttrs())
	if err != nil {
		return nil, err
	}
	if len(cols) == 1 {
		ref.Column = sql.NewIdent(cols[0])
	} else if len(cols) > 1 {
		return nil, sqlparse.Unsupported("multi-column column-level foreign key")
	}
	return ref, nil
}

// rejectFKMatchAndDeferrable errors when con specifies MATCH FULL (sql's foreign
// key has no match-type concept; MATCH FULL changes NULL-handling — a row with
// some but not all referencing columns NULL is rejected under FULL but allowed
// under the default MATCH SIMPLE) or is DEFERRABLE (see rejectDeferrable).
func rejectFKMatchAndDeferrable(con *pg.Constraint) error {
	if con.GetFkMatchtype() == "f" {
		return sqlparse.Unsupported("a foreign key with MATCH FULL")
	}
	return rejectDeferrable(con)
}

// convertForeignKey builds a table-level FOREIGN KEY constraint.
func convertForeignKey(con *pg.Constraint) (sql.ForeignKeyConstraint, error) {
	pk := con.GetPktable()
	if pk == nil {
		return sql.ForeignKeyConstraint{}, sqlparse.Unsupported("foreign key without referenced table")
	}
	if err := rejectFKMatchAndDeferrable(con); err != nil {
		return sql.ForeignKeyConstraint{}, err
	}
	columns, err := stringList(con.GetFkAttrs())
	if err != nil {
		return sql.ForeignKeyConstraint{}, err
	}
	refColumns, err := stringList(con.GetPkAttrs())
	if err != nil {
		return sql.ForeignKeyConstraint{}, err
	}
	if len(columns) != len(refColumns) {
		// An arity mismatch (or an omitted referenced-column list) has no typed
		// form the compiler accepts, so keep the statement as a raw passthrough.
		return sql.ForeignKeyConstraint{}, sqlparse.Unsupported("foreign key column-list arity mismatch")
	}
	return sql.ForeignKeyConstraint{
		Name:       sql.NewIdent(con.GetConname()),
		Columns:    sql.Idents(columns...),
		RefSchema:  sql.NewIdent(pk.GetSchemaname()),
		RefTable:   sql.NewIdentAt(pk.GetRelname(), int(pk.GetLocation())),
		RefColumns: sql.Idents(refColumns...),
		OnDelete:   fkAction(con.GetFkDelAction()),
		OnUpdate:   fkAction(con.GetFkUpdAction()),
	}, nil
}

// fkAction maps PostgreSQL's single-character referential action codes to their
// SQL clause text. "a" (NO ACTION, the default) renders nothing.
func fkAction(code string) string {
	switch code {
	case "r":
		return "RESTRICT"
	case "c":
		return "CASCADE"
	case "n":
		return "SET NULL"
	case "d":
		return "SET DEFAULT"
	default:
		return ""
	}
}

func stringList(nodes []*pg.Node) ([]string, error) {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if n.GetString_() == nil {
			return nil, sqlparse.Unsupportedf("name node %T", n.GetNode())
		}
		out = append(out, n.GetString_().GetSval())
	}
	return out, nil
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
		return sql.Table{Name: sql.NewIdent(parts[0])}, nil
	case 2:
		return sql.Table{Schema: sql.NewIdent(parts[0]), Name: sql.NewIdent(parts[1])}, nil
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
