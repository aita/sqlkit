package sqlkit_test

import (
	"database/sql/driver"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
	"github.com/aita/sqlkit/sql"
)

var pg = sql.PostgreSQLDialect{}

func assertToSQL(t *testing.T, query sql.Query, wantSQL string, wantArgs []any) {
	t.Helper()
	sqlText, args, err := query.ToSQL(pg)
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	if sqlText != wantSQL {
		t.Fatalf("ToSQL() SQL = %q,\nwant %q", sqlText, wantSQL)
	}
	if len(wantArgs) > 0 && !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("ToSQL() args = %#v, want %#v", args, wantArgs)
	}
}

func usersFake() *testutil.Fake {
	return &testutil.Fake{
		QueryFunc: func(sqlText string, args []any) (testutil.Result, error) {
			return testutil.Result{
				Columns: []string{"id", "email", "name"},
				Rows: [][]driver.Value{
					{int64(1), "a@example.com", "Alice"},
					{int64(2), "b@example.com", nil},
				},
			}, nil
		},
	}
}

// MyUserDTO is a caller-defined destination, distinct from the generated row.
type MyUserDTO struct {
	Identifier int64  `sqlkit:"id"`
	Mail       string `sqlkit:"email"`
	Name       *string
}

// insertDTO maps a subset of columns; unmapped columns are omitted.
type insertDTO struct {
	Mail string `sqlkit:"email"`
}

// UsersPosts is the has-many from users to posts (posts.user_id = users.id).
type usersPostsRel struct{ PostsTable }

func (r usersPostsRel) Target() sqlkit.Table { return r.PostsTable }

func (r usersPostsRel) JoinOn() sqlkit.Predicate {
	return sqlkit.Cond(sql.Eq(r.UserID, Users.ID))
}

func (r usersPostsRel) Cardinality() sqlkit.Cardinality { return sqlkit.ToMany }

func (r usersPostsRel) Name() string { return "Posts" }

func (r usersPostsRel) Keys() (target, base []string) { return []string{"user_id"}, []string{"id"} }

var UsersPosts = usersPostsRel{Posts}

// PostsUser is the belongs-to from posts to users (users.id = posts.user_id).
type postsUserRel struct{ UsersTable }

func (r postsUserRel) Target() sqlkit.Table { return r.UsersTable }

func (r postsUserRel) JoinOn() sqlkit.Predicate {
	return sqlkit.Cond(sql.Eq(r.ID, Posts.UserID))
}

func (r postsUserRel) Cardinality() sqlkit.Cardinality { return sqlkit.ToOne }

func (r postsUserRel) Name() string { return "User" }

func (r postsUserRel) Keys() (target, base []string) { return []string{"id"}, []string{"user_id"} }

var PostsUser = postsUserRel{Users}

var (
	_ sqlkit.Relation = UsersPosts
	_ sqlkit.Relation = PostsUser
)

// Q mirrors the generated entry point: a SELECT over the table bound to ex.
func (t UsersTable) Q(ex sqlkit.Querier) sqlkit.SelectQuery { return ex.Select().From(t) }

func (t PostsTable) Q(ex sqlkit.Querier) sqlkit.SelectQuery { return ex.Select().From(t) }

// Relations lets Preload(target) resolve a relation by the target's table.
func (t UsersTable) Relations() []sqlkit.Relation { return []sqlkit.Relation{UsersPosts} }

func (t PostsTable) Relations() []sqlkit.Relation { return []sqlkit.Relation{PostsUser, PostsComments} }

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func newDB() *sqlkit.Database { return sqlkit.WrapDB((&testutil.Fake{}).DB()) }

func assertSelectSQL(t *testing.T, q sqlkit.SelectQuery, wantSQL string, wantArgs []any) {
	t.Helper()
	sqlText, args, err := q.ToSQL(sql.PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	if sqlText != wantSQL {
		t.Fatalf("ToSQL() SQL = %q,\nwant %q", sqlText, wantSQL)
	}
	if len(wantArgs) > 0 && len(args) != len(wantArgs) {
		t.Fatalf("ToSQL() args = %#v, want %#v", args, wantArgs)
	}
}

// userFilter is a reusable filter bundle; unset (nil) terms are skipped.
type userFilter struct {
	Email sqlkit.FilterTerm
	ID    sqlkit.FilterTerm
}

// usersByPosts filters users by their related posts; Posts names the UsersPosts
// relation and carries a nested filter on the posts table.
type usersByPosts struct {
	Email sqlkit.FilterTerm
	Posts *postsByTitle
}

type postsByTitle struct {
	Title sqlkit.FilterTerm
}

// usersByPostAuthor and postsByAuthor nest a relation filter inside another
// relation filter (users → posts → users): the authors of a post written by a
// given user. Each hop must emit its own correlated EXISTS, which only works
// when a relation's Target() exposes the target's own relations — i.e. returns
// the table binding, not the bare columns value.
type usersByPostAuthor struct {
	Posts *postsByAuthor
}

type postsByAuthor struct {
	User *userFilter
}

type preUser struct {
	ID    int64 `sqlkit:"id"`
	Posts []prePost
}

type prePost struct {
	ID     int64 `sqlkit:"id"`
	UserID int64 `sqlkit:"user_id"`
	Title  string
}

type prePost2 struct {
	ID     int64 `sqlkit:"id"`
	UserID int64 `sqlkit:"user_id"`
	Title  string
	User   *preUser2
}

type preUser2 struct {
	ID   int64 `sqlkit:"id"`
	Name string
}

// postsAndUsersFake serves the posts base query and the users preload follow-up.
func postsAndUsersFake() *testutil.Fake {
	return &testutil.Fake{
		QueryFunc: func(s string, _ []any) (testutil.Result, error) {
			if contains(s, `"public"."users"`) {
				return testutil.Result{Columns: []string{"id", "name"}, Rows: [][]driver.Value{
					{int64(1), "Alice"}, {int64(2), "Bob"},
				}}, nil
			}
			return testutil.Result{Columns: []string{"id", "user_id", "title"}, Rows: [][]driver.Value{
				{int64(10), int64(1), "a"}, {int64(11), int64(2), "b"}, {int64(12), int64(1), "c"},
			}}, nil
		},
	}
}

type nestUser struct {
	ID    int64 `sqlkit:"id"`
	Name  string
	Posts []nestPost
}

type nestPost struct {
	ID     int64 `sqlkit:"id"`
	UserID int64 `sqlkit:"user_id"`
	Title  string
	User   *nestAuthor // nested to-one, loaded from each post's user_id
}

type nestAuthor struct {
	ID   int64 `sqlkit:"id"`
	Name string
}

// countQueries reports how many recorded queries mention table.
func countQueries(fake *testutil.Fake, table string) int {
	n := 0
	for _, q := range fake.Queries() {
		if contains(q.SQL, table) {
			n++
		}
	}
	return n
}

// filterTerms is a reusable filter bundle covering both an integer column (ID)
// and a string column (Email); unset (nil) terms are skipped by Filter.
type filterTerms struct {
	ID    sqlkit.FilterTerm
	Email sqlkit.FilterTerm
}

// filtered builds "SELECT * FROM users WHERE (<term>)" from a filter bundle and
// asserts the resulting SQL/args. It exercises the FilterTerm constructors,
// which resolve to a predicate against the field's column inside Filter.
func filtered(t *testing.T, f filterTerms, wantWhere string, wantArgs []any) {
	t.Helper()
	const prefix = `SELECT "public"."users"."id", "public"."users"."email", "public"."users"."name" FROM "public"."users" WHERE `
	assertSelectSQL(t, Users.Q(newDB()).Filter(f), prefix+wantWhere, wantArgs)
}

// taggedUser resolves its preload destination by a `preload:"Posts"` tag rather
// than by a field named after the relation, so the field can be named anything.
type taggedUser struct {
	ID       int64     `sqlkit:"id"`
	Authored []prePost `preload:"Posts"`
}

// toManyScalarUser declares a to-many relation field that is not a slice, which
// the preload must reject.
type toManyScalarUser struct {
	ID    int64   `sqlkit:"id"`
	Posts prePost // not a slice, though Users->Posts is to-many
}

// nullableKeyUser has a nullable (pointer) primary-key field, exercising
// scalarValue's pointer-deref and nil-skip paths during preload.
type nullableKeyUser struct {
	ID    *int64 `sqlkit:"id"`
	Posts []prePost
}

// Items exercises the write-time tag options: a JSON column, an optimistic-lock
// version column, and a database-generated read-only column. Its columns are
// id (pk), sku, meta (json), rev (version), created (read-only). None are
// defaulted, so column selection turns purely on the tag options.
type ItemsTable struct {
	info    sqlkit.TableInfo
	ID      sqlkit.Column[int64]
	SKU     sqlkit.Column[string]
	Meta    sqlkit.Column[string]
	Rev     sqlkit.Column[int64]
	Created sqlkit.Column[int64]
}

var Items = func() ItemsTable {
	info := sqlkit.TableInfo{Schema: "public", Name: "items"}
	return ItemsTable{
		info:    info,
		ID:      sqlkit.NewColumn[int64](info, "id"),
		SKU:     sqlkit.NewColumn[string](info, "sku"),
		Meta:    sqlkit.NewColumn[string](info, "meta"),
		Rev:     sqlkit.NewColumn[int64](info, "rev"),
		Created: sqlkit.NewColumn[int64](info, "created"),
	}
}()

func (t ItemsTable) Info() sqlkit.TableInfo             { return t.info }
func (t ItemsTable) SQLSource() sqlkit.SourceExpression { return t.info.SQLSource() }
func (t ItemsTable) Columns() []sqlkit.AnyColumn {
	return []sqlkit.AnyColumn{t.ID, t.SKU, t.Meta, t.Rev, t.Created}
}
func (t ItemsTable) PrimaryKey() []sqlkit.AnyColumn { return []sqlkit.AnyColumn{t.ID} }
func (t ItemsTable) Defaulted() []sqlkit.AnyColumn  { return nil }

// Item is a row DTO that opts into every write-time tag option.
type Item struct {
	ID      int64          `sqlkit:"id"`
	SKU     string         `sqlkit:"sku"`
	Meta    map[string]any `sqlkit:"meta,json"`
	Rev     int64          `sqlkit:"rev,version"`
	Created int64          `sqlkit:"created,readonly"`
}

// Events exercises the `touch` tag option: an app-maintained timestamp column
// set to CURRENT_TIMESTAMP on every INSERT and implicit SetStruct UPDATE. Its
// columns are id (pk), name, and updated_at (touch); none are defaulted.
type EventsTable struct {
	info      sqlkit.TableInfo
	ID        sqlkit.Column[int64]
	Name      sqlkit.Column[string]
	UpdatedAt sqlkit.Column[time.Time]
}

var Events = func() EventsTable {
	info := sqlkit.TableInfo{Schema: "public", Name: "events"}
	return EventsTable{
		info:      info,
		ID:        sqlkit.NewColumn[int64](info, "id"),
		Name:      sqlkit.NewColumn[string](info, "name"),
		UpdatedAt: sqlkit.NewColumn[time.Time](info, "updated_at"),
	}
}()

func (t EventsTable) Info() sqlkit.TableInfo             { return t.info }
func (t EventsTable) SQLSource() sqlkit.SourceExpression { return t.info.SQLSource() }
func (t EventsTable) Columns() []sqlkit.AnyColumn {
	return []sqlkit.AnyColumn{t.ID, t.Name, t.UpdatedAt}
}
func (t EventsTable) PrimaryKey() []sqlkit.AnyColumn { return []sqlkit.AnyColumn{t.ID} }
func (t EventsTable) Defaulted() []sqlkit.AnyColumn  { return nil }

// Event is a row DTO whose updated_at opts into the touch option.
type Event struct {
	ID        int64     `sqlkit:"id"`
	Name      string    `sqlkit:"name"`
	UpdatedAt time.Time `sqlkit:"updated_at,touch"`
}
