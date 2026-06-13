package orm_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/orm"
)

// UsersFilter is a caller-defined reusable filter bundle.
type UsersFilter struct {
	Age  orm.FilterTerm
	Name orm.FilterTerm
}

func TestFilterMapsFieldsToColumns(t *testing.T) {
	db := newDB()
	qs := Users.Queryer(db).Filter(UsersFilter{Age: orm.Le(20)})
	assertSQL(t, qs,
		`SELECT "users"."id", "users"."age", "users"."name", "users"."created_at" FROM "users" WHERE ("users"."age" <= $1)`,
		[]any{20})
}

func TestFilterMultipleFields(t *testing.T) {
	db := newDB()
	qs := Users.Queryer(db).Filter(UsersFilter{Age: orm.Le(20), Name: orm.Eq("alice")})
	gotSQL, args, err := qs.ToSQL(dialect)
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	if !strings.Contains(gotSQL, `"users"."age" <= $`) || !strings.Contains(gotSQL, `"users"."name" = $`) {
		t.Fatalf("ToSQL() missing expected predicates: %s", gotSQL)
	}
	if len(args) != 2 {
		t.Fatalf("ToSQL() args = %v, want 2", args)
	}
}

func TestFilterSkipsUnsetFields(t *testing.T) {
	db := newDB()
	// Name is left zero (nil), so only Age applies.
	qs := Users.Queryer(db).Filter(UsersFilter{Age: orm.Gt(18)})
	assertSQL(t, qs,
		`SELECT "users"."id", "users"."age", "users"."name", "users"."created_at" FROM "users" WHERE ("users"."age" > $1)`,
		[]any{18})
}

func TestFilterAcceptsRawPredicate(t *testing.T) {
	db := newDB()
	// A raw sqlkit.Predicate is applied directly.
	qs := Users.Queryer(db).Filter(Users.Age.Le(20))
	assertSQL(t, qs,
		`SELECT "users"."id", "users"."age", "users"."name", "users"."created_at" FROM "users" WHERE ("users"."age" <= $1)`,
		[]any{20})
}

func TestFilterRawPredicateInStructField(t *testing.T) {
	db := newDB()
	// A field may also hold a fully-built predicate; the field's column is
	// ignored for that field.
	qs := Users.Queryer(db).Filter(struct {
		Age sqlkit.Predicate
	}{Age: Users.Age.Le(20)})
	assertSQL(t, qs,
		`SELECT "users"."id", "users"."age", "users"."name", "users"."created_at" FROM "users" WHERE ("users"."age" <= $1)`,
		[]any{20})
}

func TestFilterTagOverride(t *testing.T) {
	db := newDB()
	qs := Users.Queryer(db).Filter(struct {
		Years orm.FilterTerm `sqlkit:"age"`
	}{Years: orm.Ge(21)})
	assertSQL(t, qs,
		`SELECT "users"."id", "users"."age", "users"."name", "users"."created_at" FROM "users" WHERE ("users"."age" >= $1)`,
		[]any{21})
}

func TestFilterUnknownFieldErrorsAtScan(t *testing.T) {
	db := newDB()
	err := Users.Queryer(db).
		Filter(struct{ Nope orm.FilterTerm }{Nope: orm.Eq(1)}).
		All(context.Background()).
		Scan(&[]struct{ ID int64 }{})
	if err == nil {
		t.Fatal("Filter with unknown field: want deferred error at Scan, got nil")
	}
	if !strings.Contains(err.Error(), "maps to no column") {
		t.Fatalf("error = %v, want 'maps to no column'", err)
	}
}

func TestFilterInAndLike(t *testing.T) {
	db := newDB()
	qs := Users.Queryer(db).Filter(struct {
		Age  orm.FilterTerm
		Name orm.FilterTerm
	}{Age: orm.In(18, 19, 20), Name: orm.Like("a%")})
	gotSQL, args, err := qs.ToSQL(dialect)
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	if !strings.Contains(gotSQL, `"users"."age" IN ($1, $2, $3)`) {
		t.Fatalf("ToSQL() missing IN: %s", gotSQL)
	}
	if !strings.Contains(gotSQL, `"users"."name" LIKE $4`) {
		t.Fatalf("ToSQL() missing LIKE: %s", gotSQL)
	}
	if len(args) != 4 {
		t.Fatalf("args = %v, want 4", args)
	}
}
