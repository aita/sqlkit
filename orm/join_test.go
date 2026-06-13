package orm_test

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/sqltest"
	"github.com/aita/sqlkit/orm"
)

// These tests complement queryset_test.go's join coverage (a single ToOne /
// ToMany inner join and one LEFT JOIN) by exercising the cases that show up in
// real queries: a LEFT JOIN on a to-one relation, multiple joins on one query,
// joins combined with the other clauses, the join-from-the-other-side relation,
// immutability across a Join, and the terminal execution paths.

func TestQuerySetLeftJoinToOne(t *testing.T) {
	db := newDB()
	qs := Users.Queryer(db).LeftJoin(Users.Rels.Profile)
	assertSQL(t, qs,
		`SELECT "users"."id", "users"."age", "users"."name", "users"."created_at" FROM "users" LEFT JOIN "profiles" ON ("profiles"."user_id" = "users"."id")`, nil)
}

func TestQuerySetMultipleJoins(t *testing.T) {
	db := newDB()
	qs := Users.Queryer(db).
		Join(Users.Rels.Posts).
		Join(Users.Rels.Profile)
	assertSQL(t, qs,
		`SELECT "users"."id", "users"."age", "users"."name", "users"."created_at" FROM "users" JOIN "posts" ON ("posts"."user_id" = "users"."id") JOIN "profiles" ON ("profiles"."user_id" = "users"."id")`, nil)
}

func TestQuerySetMixedInnerAndLeftJoins(t *testing.T) {
	db := newDB()
	qs := Users.Queryer(db).
		Join(Users.Rels.Posts).
		LeftJoin(Users.Rels.Profile)
	assertSQL(t, qs,
		`SELECT "users"."id", "users"."age", "users"."name", "users"."created_at" FROM "users" JOIN "posts" ON ("posts"."user_id" = "users"."id") LEFT JOIN "profiles" ON ("profiles"."user_id" = "users"."id")`, nil)
}

func TestQuerySetJoinFiltersBothTables(t *testing.T) {
	db := newDB()
	qs := Users.Queryer(db).
		Join(Users.Rels.Profile).
		Where(Users.Age.Ge(18)).
		Where(Users.Rels.Profile.Public.Eq(true))
	assertSQL(t, qs,
		`SELECT "users"."id", "users"."age", "users"."name", "users"."created_at" FROM "users" JOIN "profiles" ON ("profiles"."user_id" = "users"."id") WHERE (("users"."age" >= $1) AND ("profiles"."public" = $2))`,
		[]any{18, true})
}

func TestQuerySetJoinWithOrderLimitOffset(t *testing.T) {
	db := newDB()
	qs := Users.Queryer(db).
		Join(Users.Rels.Posts).
		Where(Users.Rels.Posts.Title.Eq("hello")).
		OrderBy(Users.CreatedAt.Desc()).
		Limit(10).
		Offset(5)
	assertSQL(t, qs,
		`SELECT "users"."id", "users"."age", "users"."name", "users"."created_at" FROM "users" JOIN "posts" ON ("posts"."user_id" = "users"."id") WHERE ("posts"."title" = $1) ORDER BY "users"."created_at" DESC LIMIT 10 OFFSET 5`,
		[]any{"hello"})
}

// The Profiles -> User relation joins from the side opposite to the FK; verify
// its ON clause is built from the relation alone (Profiles has no Queryer
// helper, so go through NewQuerySet directly).
func TestQuerySetProfilesUserJoin(t *testing.T) {
	db := newDB()
	qs := orm.NewQuerySet(db, Profiles).
		Join(Profiles.Rels.User).
		Where(Profiles.Public.Eq(true))
	assertSQL(t, qs,
		`SELECT "profiles"."id", "profiles"."user_id", "profiles"."bio", "profiles"."public" FROM "profiles" JOIN "users" ON ("users"."id" = "profiles"."user_id") WHERE ("profiles"."public" = $1)`,
		[]any{true})
}

// A Join returns a new QuerySet and must not mutate the receiver.
func TestQuerySetJoinImmutable(t *testing.T) {
	db := newDB()
	base := Users.Queryer(db)
	_ = base.Join(Users.Rels.Posts) // must not mutate base
	assertSQL(t, base,
		`SELECT "users"."id", "users"."age", "users"."name", "users"."created_at" FROM "users"`, nil)
}

// A joined QuerySet must carry the JOIN through to the executed statement.
func TestQuerySetJoinExecutesAll(t *testing.T) {
	var gotSQL string
	fake := &sqltest.Fake{
		QueryFunc: func(s string, args []any) (sqltest.Result, error) {
			gotSQL = s
			return sqltest.Result{
				Columns: []string{"id", "age", "name", "created_at"},
				Rows:    [][]driver.Value{{int64(1), int64(20), "alice", "now"}},
			}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	type userRow struct {
		ID        int64
		Age       int
		Name      string
		CreatedAt string
	}
	var rows []userRow
	if err := Users.Queryer(db).
		Join(Users.Rels.Posts).
		Where(Users.Rels.Posts.Title.Eq("hello")).
		All(context.Background()).Scan(&rows); err != nil {
		t.Fatalf("Join().All().Scan() error = %v", err)
	}
	if !strings.Contains(gotSQL, `JOIN "posts" ON ("posts"."user_id" = "users"."id")`) {
		t.Fatalf("executed SQL missing join clause: %s", gotSQL)
	}
	if len(rows) != 1 || rows[0].Name != "alice" {
		t.Fatalf("Join().All() rows = %+v", rows)
	}
}

// Count over a join must compile and run, preserving the JOIN.
func TestQuerySetJoinCount(t *testing.T) {
	var gotSQL string
	fake := &sqltest.Fake{
		QueryFunc: func(s string, args []any) (sqltest.Result, error) {
			gotSQL = s
			return sqltest.Result{Columns: []string{"count"}, Rows: [][]driver.Value{{int64(2)}}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	n, err := Users.Queryer(db).
		Join(Users.Rels.Profile).
		Where(Users.Rels.Profile.Public.Eq(true)).
		Count(context.Background())
	if err != nil {
		t.Fatalf("Join().Count() error = %v", err)
	}
	if n != 2 {
		t.Fatalf("Join().Count() = %d, want 2", n)
	}
	if !strings.Contains(gotSQL, `JOIN "profiles"`) {
		t.Fatalf("Count() SQL missing join clause: %s", gotSQL)
	}
}
