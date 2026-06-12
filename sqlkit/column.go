// Package sqlkit provides a typed SQL toolkit for Go: a query builder whose
// column and value types are checked at compile time, with results scanned
// into caller-defined structs.
//
// Table bindings are normally generated from a live PostgreSQL schema with
// the sqlkitgen command; the generated code implements the Table interface
// and exposes one Column[T] per database column. Queries are built from those
// bindings and materialized into a destination of your choosing:
//
//	var users []MyUserDTO
//	err := db.Select().From(appdb.Users).Where(appdb.Users.ID.Gt(10)).
//		All(ctx, db).Scan(&users)
package sqlkit

import (
	"github.com/sqlkit-dev/sqlkit/sqlkit/sql"
)

// SourceExpression aliases sql.SourceExpression so generated code only needs
// to import package sqlkit.
type SourceExpression = sql.SourceExpression

// TypedExpr is a SQL expression whose static Go type is T. Only sqlkit types
// (columns, typed values) implement it, which lets the builders reject
// type-mismatched comparisons at compile time.
type TypedExpr[T any] interface {
	sql.Expression
	typedExpr(T)
}

// AnyColumn is the type-erased view of a Column[T], used for table metadata.
type AnyColumn interface {
	sql.Expression
	sql.Selection
	// ColumnName returns the bare database column name.
	ColumnName() string
	// Reference returns the qualified SQL column reference.
	Reference() string
}

// TableInfo identifies a database table.
type TableInfo struct {
	Schema string
	Name   string
}

// QualifiedName returns the table name with an optional schema prefix.
func (t TableInfo) QualifiedName() string {
	if t.Schema == "" {
		return t.Name
	}
	return t.Schema + "." + t.Name
}

// SQLSource marks TableInfo as a sql.Source.
func (t TableInfo) SQLSource() SourceExpression {
	return SourceExpression{Name: t.QualifiedName()}
}

func (t TableInfo) sqlTable() sql.Table { return sql.Table{Schema: t.Schema, Name: t.Name} }

// Column is a typed reference to a database column. T is the Go type rows
// scan into, so predicates and assignments built from the column are
// type-checked at compile time.
type Column[T any] struct {
	table TableInfo
	name  string
}

// NewColumn creates a typed column bound to a table. It is intended for use
// by generated code.
func NewColumn[T any](table TableInfo, name string) Column[T] {
	return Column[T]{table: table, name: name}
}

func (Column[T]) SQLExpression() {}
func (Column[T]) SQLSelection()  {}
func (Column[T]) typedExpr(T)    {}

// ColumnName returns the bare database column name.
func (c Column[T]) ColumnName() string { return c.name }

// Reference returns the qualified SQL column reference.
func (c Column[T]) Reference() string {
	if c.table.Name == "" {
		return c.name
	}
	return c.table.QualifiedName() + "." + c.name
}

// Eq creates a "column = value" predicate.
func (c Column[T]) Eq(value T) Predicate { return newPredicate(sql.Eq(c, sql.Val(value))) }

// EqExpr creates a "column = expression" predicate, e.g. for join conditions.
func (c Column[T]) EqExpr(other TypedExpr[T]) Predicate { return newPredicate(sql.Eq(c, other)) }

// Ne creates a "column <> value" predicate.
func (c Column[T]) Ne(value T) Predicate { return newPredicate(sql.Ne(c, sql.Val(value))) }

// NeExpr creates a "column <> expression" predicate.
func (c Column[T]) NeExpr(other TypedExpr[T]) Predicate { return newPredicate(sql.Ne(c, other)) }

// Lt creates a "column < value" predicate.
func (c Column[T]) Lt(value T) Predicate { return newPredicate(sql.Lt(c, sql.Val(value))) }

// Le creates a "column <= value" predicate.
func (c Column[T]) Le(value T) Predicate { return newPredicate(sql.Le(c, sql.Val(value))) }

// Gt creates a "column > value" predicate.
func (c Column[T]) Gt(value T) Predicate { return newPredicate(sql.Gt(c, sql.Val(value))) }

// Ge creates a "column >= value" predicate.
func (c Column[T]) Ge(value T) Predicate { return newPredicate(sql.Ge(c, sql.Val(value))) }

// In creates a "column IN (values...)" predicate.
func (c Column[T]) In(values ...T) Predicate {
	items := make([]any, len(values))
	for i, value := range values {
		items[i] = sql.Val(value)
	}
	return newPredicate(sql.In(c, items...))
}

// NotIn creates a "column NOT IN (values...)" predicate.
func (c Column[T]) NotIn(values ...T) Predicate {
	items := make([]any, len(values))
	for i, value := range values {
		items[i] = sql.Val(value)
	}
	return newPredicate(sql.NotIn(c, items...))
}

// IsNull creates a "column IS NULL" predicate.
func (c Column[T]) IsNull() Predicate { return newPredicate(sql.IsNull(c)) }

// IsNotNull creates a "column IS NOT NULL" predicate.
func (c Column[T]) IsNotNull() Predicate { return newPredicate(sql.IsNotNull(c)) }

// Asc creates an ascending ORDER BY term.
func (c Column[T]) Asc() sql.OrderTerm { return sql.Asc(c) }

// Desc creates a descending ORDER BY term.
func (c Column[T]) Desc() sql.OrderTerm { return sql.Desc(c) }

// Set creates a typed SET assignment for UPDATE statements.
func (c Column[T]) Set(value T) sql.Assignment { return sql.Set(c.name, sql.Val(value)) }

// SetExpr creates a SET assignment from another typed expression.
func (c Column[T]) SetExpr(value TypedExpr[T]) sql.Assignment { return sql.Set(c.name, value) }

// V creates a typed bind value expression.
func V[T any](value T) TypedExpr[T] { return typedValue[T]{sql.Val(value)} }

type typedValue[T any] struct{ sql.ValueExpression }

func (typedValue[T]) typedExpr(T) {}

// UnwrapExpression exposes the underlying value expression to the compiler.
func (v typedValue[T]) UnwrapExpression() sql.Expression { return v.ValueExpression }

// Like creates a LIKE predicate for string expressions.
func Like(expr TypedExpr[string], pattern string) Predicate {
	return newPredicate(sql.Like(expr, sql.Val(pattern)))
}
