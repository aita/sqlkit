package sql

// Query is implemented by SQL statements that can be compiled.
type Query interface {
	ToSQL(dialect Dialect) (string, []any, error)
}

// compilable is implemented by statements that can compile through a shared
// compiler, keeping bind placeholder numbering consistent across subqueries.
type compilable interface {
	compile(c *Compiler) (string, []any, error)
}

// SelectStmt is the immutable AST for a SELECT statement. Every field is
// exported: this struct is the package's read-only public contract, traversed
// and compiled directly with no Parts() snapshot. Build one with the fluent
// SelectBuilder (sql.Select) or, from a parser, as a struct literal. A nil
// Where/Having/From or an empty slice means the clause is absent; HasLimit /
// HasOffset distinguish a zero LIMIT/OFFSET from none.
type SelectStmt struct {
	posField
	With       []CTE
	From       Source
	Columns    []Selection
	Joins      []JoinClause
	Where      Expression
	GroupBy    []Expression
	Having     Expression
	OrderBy    []OrderTerm
	Limit      int
	HasLimit   bool
	Offset     int
	HasOffset  bool
	Distinct   bool
	DistinctOn []Expression
	Windows    []NamedWindow
	Lock       *lockClause
}

// lockClause is a SELECT row-locking clause: a strength (UPDATE / SHARE / NO KEY
// UPDATE / KEY SHARE), an optional OF table list, and an optional wait mode
// (NOWAIT / SKIP LOCKED).
type lockClause struct {
	posField
	strength string
	of       []Ident
	wait     string
}

// JoinClause represents a JOIN clause. Its condition is given in exactly one
// way: On for a JOIN ... ON predicate, Using for a JOIN ... USING (columns) on
// equally named columns, or Natural for a NATURAL JOIN that matches every
// equally named column implicitly (in which case On and Using are empty). A
// CROSS JOIN carries none of them.
type JoinClause struct {
	posField
	Kind    string
	Source  Source
	On      Expression
	Using   []Ident
	Natural bool
}

// SQLSource marks SelectStmt as a subquery source.
func (q SelectStmt) SQLSource() SourceExpression { return SourceExpression{Query: q} }

// ToSQL compiles the SELECT statement.
func (q SelectStmt) ToSQL(dialect Dialect) (string, []any, error) {
	return q.compile(NewCompiler(dialect))
}

func (q SelectStmt) compile(c *Compiler) (string, []any, error) {
	return c.CompileSelect(q)
}

func (q SelectStmt) setPos(pos int) Query { q.NodePos = pos; return q }

// SelectBuilder is the fluent constructor for a SelectStmt. It is a separate
// type from the AST because the exported field SelectStmt.Where and a same-named
// builder method cannot coexist in Go; construction lives here and the data on
// SelectStmt. Each method copies the in-progress statement and returns a new
// builder, so a builder value can be branched and reused. Build (or any terminal
// such as ToSQL) yields the finished SelectStmt.
type SelectBuilder struct{ s SelectStmt }

// Select creates a SELECT query builder.
func Select(columns ...Selection) SelectBuilder {
	return SelectBuilder{s: SelectStmt{Columns: columns}}
}

// Stmt returns the built SELECT statement — the pure AST value.
func (b SelectBuilder) Stmt() SelectStmt { return b.s }

// AddColumns appends items to the SELECT list, keeping the existing projection.
// It is used to extend a projection after From has expanded it (e.g. adding a
// joined table's columns), without rebuilding the query.
func (b SelectBuilder) AddColumns(columns ...Selection) SelectBuilder {
	b.s.Columns = append(append([]Selection(nil), b.s.Columns...), columns...)
	return b
}

// Distinct marks the query as SELECT DISTINCT.
func (b SelectBuilder) Distinct() SelectBuilder {
	b.s.Distinct = true
	return b
}

// DistinctOn marks the query as SELECT DISTINCT ON (exprs...), keeping the first
// row of each group of rows equal on the given expressions (PostgreSQL). It is a
// PostgreSQL extension: dialects without it reject the query at compile time.
// Pair it with an ORDER BY that begins with the same expressions to make "first"
// deterministic.
func (b SelectBuilder) DistinctOn(exprs ...Expression) SelectBuilder {
	b.s.DistinctOn = append(append([]Expression(nil), b.s.DistinctOn...), exprs...)
	return b
}

// From sets the table or subquery for the SELECT query.
func (b SelectBuilder) From(source Source) SelectBuilder {
	b.s.From = source
	return b
}

// Join adds an INNER JOIN clause.
func (b SelectBuilder) Join(source Source, on Expression) SelectBuilder {
	b.s.Joins = appendJoin(b.s.Joins, JoinClause{Kind: "JOIN", Source: source, On: on})
	return b
}

// LeftJoin adds a LEFT JOIN clause.
func (b SelectBuilder) LeftJoin(source Source, on Expression) SelectBuilder {
	b.s.Joins = appendJoin(b.s.Joins, JoinClause{Kind: "LEFT JOIN", Source: source, On: on})
	return b
}

// RightJoin adds a RIGHT JOIN clause.
func (b SelectBuilder) RightJoin(source Source, on Expression) SelectBuilder {
	b.s.Joins = appendJoin(b.s.Joins, JoinClause{Kind: "RIGHT JOIN", Source: source, On: on})
	return b
}

// FullJoin adds a FULL OUTER JOIN clause.
func (b SelectBuilder) FullJoin(source Source, on Expression) SelectBuilder {
	b.s.Joins = appendJoin(b.s.Joins, JoinClause{Kind: "FULL JOIN", Source: source, On: on})
	return b
}

// CrossJoin adds a CROSS JOIN clause. A cross join has no ON condition; it
// produces the Cartesian product of its inputs.
func (b SelectBuilder) CrossJoin(source Source) SelectBuilder {
	b.s.Joins = appendJoin(b.s.Joins, JoinClause{Kind: "CROSS JOIN", Source: source})
	return b
}

// JoinUsing adds an INNER JOIN ... USING (columns) clause, matching rows on the
// equally named columns (which collapse to a single output column each).
func (b SelectBuilder) JoinUsing(source Source, columns ...string) SelectBuilder {
	b.s.Joins = appendJoin(b.s.Joins, JoinClause{Kind: "JOIN", Source: source, Using: idents(columns)})
	return b
}

// LeftJoinUsing adds a LEFT JOIN ... USING (columns) clause.
func (b SelectBuilder) LeftJoinUsing(source Source, columns ...string) SelectBuilder {
	b.s.Joins = appendJoin(b.s.Joins, JoinClause{Kind: "LEFT JOIN", Source: source, Using: idents(columns)})
	return b
}

// NaturalJoin adds a NATURAL JOIN clause, an inner join on every column the two
// sources share by name. It takes no condition.
func (b SelectBuilder) NaturalJoin(source Source) SelectBuilder {
	b.s.Joins = appendJoin(b.s.Joins, JoinClause{Kind: "JOIN", Source: source, Natural: true})
	return b
}

// NaturalLeftJoin adds a NATURAL LEFT JOIN clause.
func (b SelectBuilder) NaturalLeftJoin(source Source) SelectBuilder {
	b.s.Joins = appendJoin(b.s.Joins, JoinClause{Kind: "LEFT JOIN", Source: source, Natural: true})
	return b
}

// Where adds a WHERE predicate, combined with AND when called repeatedly.
func (b SelectBuilder) Where(expr Expression) SelectBuilder {
	if b.s.Where == nil {
		b.s.Where = expr
	} else {
		b.s.Where = And(b.s.Where, expr)
	}
	return b
}

// GroupBy adds GROUP BY expressions.
func (b SelectBuilder) GroupBy(exprs ...Expression) SelectBuilder {
	b.s.GroupBy = append(append([]Expression(nil), b.s.GroupBy...), exprs...)
	return b
}

// Having sets the HAVING predicate.
func (b SelectBuilder) Having(expr Expression) SelectBuilder {
	b.s.Having = expr
	return b
}

// Window adds named window definitions to the SELECT's WINDOW clause, referenced
// from an OVER clause with WindowExpression.Named, e.g.
// Window(WindowDef("w").PartitionBy(Col("team")).OrderBy(Desc(Col("score")))).
func (b SelectBuilder) Window(defs ...NamedWindow) SelectBuilder {
	b.s.Windows = append(append([]NamedWindow(nil), b.s.Windows...), defs...)
	return b
}

// OrderBy adds ORDER BY terms.
func (b SelectBuilder) OrderBy(terms ...OrderTerm) SelectBuilder {
	b.s.OrderBy = append(append([]OrderTerm(nil), b.s.OrderBy...), terms...)
	return b
}

// Limit sets the LIMIT clause.
func (b SelectBuilder) Limit(n int) SelectBuilder {
	b.s.Limit = n
	b.s.HasLimit = true
	return b
}

// Offset sets the OFFSET clause.
func (b SelectBuilder) Offset(n int) SelectBuilder {
	b.s.Offset = n
	b.s.HasOffset = true
	return b
}

// LockOption refines a SELECT row-locking clause (ForUpdate / ForShare / …):
// SkipLocked, NoWait, and LockOf.
type LockOption func(*lockClause)

// SkipLocked adds SKIP LOCKED, skipping rows another transaction has already
// locked rather than waiting (the queue-worker pattern).
func SkipLocked() LockOption { return func(l *lockClause) { l.wait = "SKIP LOCKED" } }

// NoWait adds NOWAIT, erroring immediately instead of waiting when a selected
// row is already locked.
func NoWait() LockOption { return func(l *lockClause) { l.wait = "NOWAIT" } }

// LockOf restricts the lock to rows from the named tables/aliases
// (FOR UPDATE OF t), for a join that should lock only some of its inputs.
func LockOf(tables ...string) LockOption {
	return func(l *lockClause) { l.of = append(l.of, idents(tables)...) }
}

// ForUpdate adds a FOR UPDATE row-locking clause, locking the selected rows
// against concurrent updates and deletes until the transaction ends. Refine it
// with the SkipLocked / NoWait / LockOf options. Row locking is PostgreSQL/MySQL
// (8.0+) only; other dialects reject it at compile time.
func (b SelectBuilder) ForUpdate(opts ...LockOption) SelectBuilder { return b.lockWith("UPDATE", opts) }

// ForShare adds a FOR SHARE row-locking clause, a shared lock that blocks
// concurrent writes but not other shared locks.
func (b SelectBuilder) ForShare(opts ...LockOption) SelectBuilder { return b.lockWith("SHARE", opts) }

// ForNoKeyUpdate adds a FOR NO KEY UPDATE clause, a weaker FOR UPDATE that does
// not block concurrent inserts of referencing rows (PostgreSQL only).
func (b SelectBuilder) ForNoKeyUpdate(opts ...LockOption) SelectBuilder {
	return b.lockWith("NO KEY UPDATE", opts)
}

// ForKeyShare adds a FOR KEY SHARE clause, the weakest lock, blocking only
// changes to the key columns (PostgreSQL only).
func (b SelectBuilder) ForKeyShare(opts ...LockOption) SelectBuilder {
	return b.lockWith("KEY SHARE", opts)
}

func (b SelectBuilder) lockWith(strength string, opts []LockOption) SelectBuilder {
	lock := &lockClause{strength: strength}
	for _, opt := range opts {
		opt(lock)
	}
	b.s.Lock = lock
	return b
}

// SQLSource marks the building SELECT as a subquery source, so a builder can be
// dropped straight into a FROM/JOIN position.
func (b SelectBuilder) SQLSource() SourceExpression { return b.s.SQLSource() }

// ToSQL compiles the building SELECT statement.
func (b SelectBuilder) ToSQL(dialect Dialect) (string, []any, error) { return b.s.ToSQL(dialect) }

func (b SelectBuilder) compile(c *Compiler) (string, []any, error) { return b.s.compile(c) }

func (b SelectBuilder) buildStmt() Query { return b.s }

func (b SelectBuilder) setPos(pos int) Query { b.s.NodePos = pos; return b }

// appendJoin returns joins with j appended to a fresh backing array, so chaining
// from a shared builder value never disturbs a sibling chain.
func appendJoin(joins []JoinClause, j JoinClause) []JoinClause {
	return append(append([]JoinClause(nil), joins...), j)
}
