package sqlkit_test

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/sqltest"
	"github.com/aita/sqlkit/sql"
)

// Relation fixtures over the Users/Posts bindings from session_test.go, mirroring
// what codegen emits: a value implementing sqlkit.Relation per foreign key.

// UsersPosts is the has-many from users to posts (posts.user_id = users.id).
type usersPostsRel struct{ PostsTable }

func (r usersPostsRel) Target() sqlkit.Table { return r.PostsTable }
func (r usersPostsRel) JoinOn() sqlkit.Predicate {
	return sqlkit.Cond(sql.Eq(r.PostsTable.UserID, Users.ID))
}
func (r usersPostsRel) Cardinality() sqlkit.Cardinality { return sqlkit.ToMany }
func (r usersPostsRel) Name() string                    { return "Posts" }
func (r usersPostsRel) Keys() (target, base []string)   { return []string{"user_id"}, []string{"id"} }

var UsersPosts = usersPostsRel{Posts}

// PostsUser is the belongs-to from posts to users (users.id = posts.user_id).
type postsUserRel struct{ UsersTable }

func (r postsUserRel) Target() sqlkit.Table { return r.UsersTable }
func (r postsUserRel) JoinOn() sqlkit.Predicate {
	return sqlkit.Cond(sql.Eq(r.UsersTable.ID, Posts.UserID))
}
func (r postsUserRel) Cardinality() sqlkit.Cardinality { return sqlkit.ToOne }
func (r postsUserRel) Name() string                    { return "User" }
func (r postsUserRel) Keys() (target, base []string)   { return []string{"id"}, []string{"user_id"} }

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
func (t PostsTable) Relations() []sqlkit.Relation { return []sqlkit.Relation{PostsUser} }

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func newDB() *sqlkit.Database { return sqlkit.WrapDB((&sqltest.Fake{}).DB()) }

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

func TestSelectJoinRel(t *testing.T) {
	db := newDB()
	assertSelectSQL(t, Posts.Q(db).JoinRel(PostsUser).Where(Users.Email.Eq("a@x")),
		`SELECT "public"."posts"."id", "public"."posts"."user_id", "public"."posts"."title" FROM "public"."posts" JOIN "public"."users" ON ("public"."users"."id" = "public"."posts"."user_id") WHERE ("public"."users"."email" = $1)`,
		[]any{"a@x"})
}

func TestSelectLeftJoinRel(t *testing.T) {
	db := newDB()
	assertSelectSQL(t, Users.Q(db).LeftJoinRel(UsersPosts),
		`SELECT "public"."users"."id", "public"."users"."email", "public"."users"."name" FROM "public"."users" LEFT JOIN "public"."posts" ON ("public"."posts"."user_id" = "public"."users"."id")`,
		nil)
}

func TestSelectRightAndFullJoinRel(t *testing.T) {
	db := newDB()
	assertSelectSQL(t, Users.Q(db).RightJoinRel(UsersPosts),
		`SELECT "public"."users"."id", "public"."users"."email", "public"."users"."name" FROM "public"."users" RIGHT JOIN "public"."posts" ON ("public"."posts"."user_id" = "public"."users"."id")`,
		nil)
	assertSelectSQL(t, Users.Q(db).FullJoinRel(UsersPosts),
		`SELECT "public"."users"."id", "public"."users"."email", "public"."users"."name" FROM "public"."users" FULL JOIN "public"."posts" ON ("public"."posts"."user_id" = "public"."users"."id")`,
		nil)
}

// userFilter is a reusable filter bundle; unset (nil) terms are skipped.
type userFilter struct {
	Email sqlkit.FilterTerm
	ID    sqlkit.FilterTerm
}

func TestSelectFilter(t *testing.T) {
	db := newDB()
	assertSelectSQL(t, Users.Q(db).Filter(userFilter{Email: sqlkit.Eq("a@x")}),
		`SELECT "public"."users"."id", "public"."users"."email", "public"."users"."name" FROM "public"."users" WHERE ("public"."users"."email" = $1)`,
		[]any{"a@x"})
}

func TestSelectFilterRequiresFromTable(t *testing.T) {
	db := newDB()
	// A subquery source is not a table, so Filter defers an error to the terminal.
	q := sqlkit.Select().From(sqlkit.Select().From(Users).SQLSource()).Filter(userFilter{ID: sqlkit.Eq(1)})
	if _, _, err := q.ToSQL(sql.PostgreSQLDialect{}); err == nil {
		t.Fatal("Filter without a From table should defer an error")
	}
	_ = db
}

func TestSelectGet(t *testing.T) {
	db := newDB()
	assertSelectSQL(t, Users.Q(db).Where(Users.Email.Eq("x")),
		`SELECT "public"."users"."id", "public"."users"."email", "public"."users"."name" FROM "public"."users" WHERE ("public"."users"."email" = $1)`,
		[]any{"x"})
	// Get adds the PK predicate and LIMIT 1.
	rows := Users.Q(db).Get(context.Background(), 7)
	if err := rows.Scan(&User{}); err != nil && err != sqlkit.ErrNoRows {
		t.Fatalf("Get() scan error = %v", err)
	}
}

func TestSelectGetWrongArity(t *testing.T) {
	db := newDB()
	if err := Users.Q(db).Get(context.Background(), 1, 2).Scan(&User{}); err == nil {
		t.Fatal("Get with too many primary-key values should error")
	}
}

// --- Preload ---

type preUser struct {
	ID    int64 `sqlkit:"id"`
	Posts []prePost
}

type prePost struct {
	ID     int64 `sqlkit:"id"`
	UserID int64 `sqlkit:"user_id"`
	Title  string
}

func TestPreloadToManyPopulatesSliceField(t *testing.T) {
	// The base query returns the three users; the preload follow-up returns the
	// posts. The fake serves each by inspecting the SQL.
	fake := &sqltest.Fake{
		QueryFunc: func(s string, _ []any) (sqltest.Result, error) {
			if contains(s, `"public"."posts"`) {
				return sqltest.Result{Columns: []string{"id", "user_id", "title"}, Rows: [][]driver.Value{
					{int64(10), int64(1), "a"},
					{int64(11), int64(1), "b"},
					{int64(12), int64(2), "c"},
				}}, nil
			}
			return sqltest.Result{Columns: []string{"id"}, Rows: [][]driver.Value{
				{int64(1)}, {int64(2)}, {int64(3)},
			}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	var users []preUser
	if err := db.Select(Users.ID).From(Users).Preload(Posts).All(context.Background()).Scan(&users); err != nil {
		t.Fatalf("Preload chain error = %v", err)
	}
	if len(users) != 3 {
		t.Fatalf("scanned users = %+v", users)
	}
	if len(users[0].Posts) != 2 || users[0].Posts[0].Title != "a" || users[0].Posts[1].Title != "b" {
		t.Fatalf("user 1 posts = %+v", users[0].Posts)
	}
	if len(users[1].Posts) != 1 || users[1].Posts[0].Title != "c" {
		t.Fatalf("user 2 posts = %+v", users[1].Posts)
	}
	if len(users[2].Posts) != 0 {
		t.Fatalf("user 3 should have no posts, got %+v", users[2].Posts)
	}
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
func postsAndUsersFake() *sqltest.Fake {
	return &sqltest.Fake{
		QueryFunc: func(s string, _ []any) (sqltest.Result, error) {
			if contains(s, `"public"."users"`) {
				return sqltest.Result{Columns: []string{"id", "name"}, Rows: [][]driver.Value{
					{int64(1), "Alice"}, {int64(2), "Bob"},
				}}, nil
			}
			return sqltest.Result{Columns: []string{"id", "user_id", "title"}, Rows: [][]driver.Value{
				{int64(10), int64(1), "a"}, {int64(11), int64(2), "b"}, {int64(12), int64(1), "c"},
			}}, nil
		},
	}
}

func TestPreloadToOnePopulatesPointerField(t *testing.T) {
	db := sqlkit.WrapDB(postsAndUsersFake().DB())
	var posts []prePost2
	if err := db.Select().From(Posts).Preload(Users).All(context.Background()).Scan(&posts); err != nil {
		t.Fatalf("Preload chain error = %v", err)
	}
	if len(posts) != 3 {
		t.Fatalf("scanned posts = %+v", posts)
	}
	if posts[0].User == nil || posts[0].User.Name != "Alice" {
		t.Fatalf("post 10 user = %+v", posts[0].User)
	}
	if posts[2].User == nil || posts[2].User.Name != "Alice" {
		t.Fatalf("post 12 user = %+v", posts[2].User)
	}
}

// Preload also runs for a single-row terminal (First), populating one parent.
func TestPreloadSingleStructViaFirst(t *testing.T) {
	db := sqlkit.WrapDB(postsAndUsersFake().DB())
	var post prePost2
	if err := db.Select().From(Posts).Preload(Users).First(context.Background()).Scan(&post); err != nil {
		t.Fatalf("Preload First error = %v", err)
	}
	if post.User == nil || post.User.Name != "Alice" {
		t.Fatalf("single post user = %+v", post.User)
	}
}

func TestPreloadEmptyParentsIsNoop(t *testing.T) {
	fake := &sqltest.Fake{QueryFunc: func(string, []any) (sqltest.Result, error) {
		return sqltest.Result{Columns: []string{"id"}}, nil // no rows
	}}
	db := sqlkit.WrapDB(fake.DB())
	var users []preUser
	if err := db.Select(Users.ID).From(Users).Preload(Posts).All(context.Background()).Scan(&users); err != nil {
		t.Fatalf("Preload on empty result error = %v", err)
	}
}

func TestPreloadMissingFieldErrors(t *testing.T) {
	type noPostsUser struct {
		ID int64 `sqlkit:"id"`
	}
	fake := &sqltest.Fake{QueryFunc: func(string, []any) (sqltest.Result, error) {
		return sqltest.Result{Columns: []string{"id"}, Rows: [][]driver.Value{{int64(1)}}}, nil
	}}
	db := sqlkit.WrapDB(fake.DB())
	var users []noPostsUser
	err := db.Select(Users.ID).From(Users).Preload(Posts).All(context.Background()).Scan(&users)
	if err == nil {
		t.Fatal("Preload should error when no destination field matches the relation")
	}
}

// Preload(target) errors when the base table has no relation reaching target.
func TestPreloadNoRelationErrors(t *testing.T) {
	db := newDB()
	// Users has a relation to Posts but not to itself.
	err := db.Select(Users.ID).From(Users).Preload(Users).All(context.Background()).Scan(&[]preUser{})
	if err == nil {
		t.Fatal("Preload to an unrelated table should error")
	}
}

// PreloadRel takes the relation by value, for disambiguation or hand-built relations.
func TestPreloadRelExplicit(t *testing.T) {
	db := sqlkit.WrapDB(postsAndUsersFake().DB())
	var posts []prePost2
	if err := db.Select().From(Posts).PreloadRel(PostsUser).All(context.Background()).Scan(&posts); err != nil {
		t.Fatalf("PreloadRel error = %v", err)
	}
	if len(posts) == 0 || posts[0].User == nil || posts[0].User.Name != "Alice" {
		t.Fatalf("PreloadRel did not populate: %+v", posts)
	}
}

// --- nested & conditional Preload ---

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

// TestPreloadNested loads Users -> Posts -> (each post's) User in three batched
// queries and threads the grandchildren onto the value-type child slice.
func TestPreloadNested(t *testing.T) {
	fake := &sqltest.Fake{
		QueryFunc: func(s string, _ []any) (sqltest.Result, error) {
			if contains(s, `"public"."posts"`) {
				return sqltest.Result{Columns: []string{"id", "user_id", "title"}, Rows: [][]driver.Value{
					{int64(10), int64(1), "a"}, {int64(11), int64(2), "b"},
				}}, nil
			}
			// Both the base users query and the nested users follow-up land here.
			return sqltest.Result{Columns: []string{"id", "name"}, Rows: [][]driver.Value{
				{int64(1), "Alice"}, {int64(2), "Bob"},
			}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	var users []nestUser
	err := db.Select().From(Users).
		Preload(sqlkit.RelPath(Posts, Users)).
		All(context.Background()).Scan(&users)
	if err != nil {
		t.Fatalf("nested Preload error = %v", err)
	}
	if len(users) != 2 || len(users[0].Posts) != 1 {
		t.Fatalf("scanned users = %+v", users)
	}
	post := users[0].Posts[0]
	if post.User == nil || post.User.Name != "Alice" {
		t.Fatalf("nested user not loaded onto child copy: %+v", post.User)
	}
	if users[1].Posts[0].User == nil || users[1].Posts[0].User.Name != "Bob" {
		t.Fatalf("nested user for second branch = %+v", users[1].Posts[0].User)
	}
}

// TestPreloadConditionalAppliesWhere checks a Preload condition narrows the
// follow-up SELECT while leaving the parent-key IN as the (still splittable)
// deferred filter.
func TestPreloadConditionalAppliesWhere(t *testing.T) {
	var postsSQL string
	fake := &sqltest.Fake{QueryFunc: func(s string, _ []any) (sqltest.Result, error) {
		if contains(s, `"public"."posts"`) {
			postsSQL = s
			return sqltest.Result{Columns: []string{"id", "user_id", "title"}, Rows: [][]driver.Value{
				{int64(10), int64(1), "a"},
			}}, nil
		}
		return sqltest.Result{Columns: []string{"id"}, Rows: [][]driver.Value{{int64(1)}}}, nil
	}}
	db := sqlkit.WrapDB(fake.DB())

	var users []preUser
	err := db.Select(Users.ID).From(Users).
		Preload(Posts, Posts.Title.Eq("a")).
		All(context.Background()).Scan(&users)
	if err != nil {
		t.Fatalf("conditional Preload error = %v", err)
	}
	if !contains(postsSQL, `"public"."posts"."title"`) || !contains(postsSQL, "IN") {
		t.Fatalf("conditional preload SQL missing condition or IN: %q", postsSQL)
	}
	if len(users) != 1 || len(users[0].Posts) != 1 || users[0].Posts[0].Title != "a" {
		t.Fatalf("scanned users = %+v", users)
	}
}

// A RelPath whose hops are not related defers an error.
func TestPreloadRelPathUnrelatedHopErrors(t *testing.T) {
	db := newDB()
	// Users relates to Posts, but Posts has no relation to Posts, so the second
	// hop cannot resolve.
	err := db.Select().From(Users).
		Preload(sqlkit.RelPath(Posts, Posts)).
		All(context.Background()).Scan(&[]nestUser{})
	if err == nil {
		t.Fatal("RelPath with an unrelated hop should defer an error")
	}
}

// countQueries reports how many recorded queries mention table.
func countQueries(fake *sqltest.Fake, table string) int {
	n := 0
	for _, q := range fake.Queries() {
		if contains(q.SQL, table) {
			n++
		}
	}
	return n
}

// TestPreloadRelPathReusesRegisteredLevel checks that a table named by both a
// plain Preload and a RelPath hop is loaded once, with the deeper hop nested under
// the existing spec rather than duplicated.
func TestPreloadRelPathReusesRegisteredLevel(t *testing.T) {
	fake := &sqltest.Fake{QueryFunc: func(s string, _ []any) (sqltest.Result, error) {
		if contains(s, `"public"."posts"`) {
			return sqltest.Result{Columns: []string{"id", "user_id", "title"}, Rows: [][]driver.Value{
				{int64(10), int64(1), "a"}, {int64(11), int64(2), "b"},
			}}, nil
		}
		return sqltest.Result{Columns: []string{"id", "name"}, Rows: [][]driver.Value{
			{int64(1), "Alice"}, {int64(2), "Bob"},
		}}, nil
	}}
	db := sqlkit.WrapDB(fake.DB())

	var users []nestUser
	err := db.Select().From(Users).
		Preload(Posts).
		Preload(sqlkit.RelPath(Posts, Users)).
		All(context.Background()).Scan(&users)
	if err != nil {
		t.Fatalf("RelPath reuse error = %v", err)
	}
	if n := countQueries(fake, `"public"."posts"`); n != 1 {
		t.Fatalf("posts queried %d times, want 1 (the RelPath hop should reuse the Preload(Posts) spec)", n)
	}
	if len(users) != 2 || len(users[0].Posts) != 1 || users[0].Posts[0].User == nil ||
		users[0].Posts[0].User.Name != "Alice" {
		t.Fatalf("nested user not loaded under reused Posts: %+v", users)
	}
}

// TestPreloadRelPathConditionOnFinalHop checks Preload conditions apply only to
// the last table on a RelPath, not the intermediate hops.
func TestPreloadRelPathConditionOnFinalHop(t *testing.T) {
	var postsSQL, nestedUsersSQL string
	fake := &sqltest.Fake{QueryFunc: func(s string, _ []any) (sqltest.Result, error) {
		if contains(s, `"public"."posts"`) {
			postsSQL = s
			return sqltest.Result{Columns: []string{"id", "user_id", "title"}, Rows: [][]driver.Value{
				{int64(10), int64(1), "a"},
			}}, nil
		}
		if contains(s, "IN") { // the nested users follow-up carries the parent-key IN
			nestedUsersSQL = s
		}
		return sqltest.Result{Columns: []string{"id", "name"}, Rows: [][]driver.Value{{int64(1), "Alice"}}}, nil
	}}
	db := sqlkit.WrapDB(fake.DB())

	var users []nestUser
	err := db.Select().From(Users).
		Preload(sqlkit.RelPath(Posts, Users), Users.Email.Eq("z@x")).
		All(context.Background()).Scan(&users)
	if err != nil {
		t.Fatalf("RelPath condition error = %v", err)
	}
	if contains(postsSQL, `"email"`) {
		t.Fatalf("condition leaked onto the intermediate Posts hop: %q", postsSQL)
	}
	if !contains(nestedUsersSQL, `"public"."users"."email"`) {
		t.Fatalf("condition missing on the final Users hop: %q", nestedUsersSQL)
	}
}

// TestPreloadRelSingleTable checks a one-element RelPath behaves like Preload(t).
func TestPreloadRelPathSingleTable(t *testing.T) {
	fake := &sqltest.Fake{QueryFunc: func(s string, _ []any) (sqltest.Result, error) {
		if contains(s, `"public"."posts"`) {
			return sqltest.Result{Columns: []string{"id", "user_id", "title"}, Rows: [][]driver.Value{
				{int64(10), int64(1), "a"},
			}}, nil
		}
		return sqltest.Result{Columns: []string{"id"}, Rows: [][]driver.Value{{int64(1)}}}, nil
	}}
	db := sqlkit.WrapDB(fake.DB())

	var users []preUser
	if err := db.Select(Users.ID).From(Users).
		Preload(sqlkit.RelPath(Posts)).
		All(context.Background()).Scan(&users); err != nil {
		t.Fatalf("single-table RelPath error = %v", err)
	}
	if len(users) != 1 || len(users[0].Posts) != 1 {
		t.Fatalf("single-table RelPath did not load posts: %+v", users)
	}
}

// TestPreloadEmptyRelPathErrors checks RelPath() defers an error.
func TestPreloadEmptyRelPathErrors(t *testing.T) {
	db := newDB()
	err := db.Select().From(Users).Preload(sqlkit.RelPath()).
		All(context.Background()).Scan(&[]preUser{})
	if err == nil {
		t.Fatal("Preload(RelPath()) should defer an error")
	}
}

// TestPreloadRelAppliesCondition checks PreloadRel takes conditions too.
func TestPreloadRelAppliesCondition(t *testing.T) {
	var usersSQL string
	fake := &sqltest.Fake{QueryFunc: func(s string, _ []any) (sqltest.Result, error) {
		if contains(s, `"public"."users"`) {
			usersSQL = s
			return sqltest.Result{Columns: []string{"id", "name"}, Rows: [][]driver.Value{{int64(1), "Alice"}}}, nil
		}
		return sqltest.Result{Columns: []string{"id", "user_id", "title"}, Rows: [][]driver.Value{
			{int64(10), int64(1), "a"},
		}}, nil
	}}
	db := sqlkit.WrapDB(fake.DB())

	var posts []prePost2
	if err := db.Select().From(Posts).
		PreloadRel(PostsUser, Users.Email.Eq("a@x")).
		All(context.Background()).Scan(&posts); err != nil {
		t.Fatalf("PreloadRel condition error = %v", err)
	}
	if !contains(usersSQL, `"public"."users"."email"`) {
		t.Fatalf("PreloadRel condition not applied: %q", usersSQL)
	}
}

// TestPreloadRequiresFromTable checks Preload defers an error when the source is
// not a table (e.g. a subquery), so no relation can be resolved.
func TestPreloadRequiresFromTable(t *testing.T) {
	db := newDB()
	err := db.Select().From(sqlkit.Select().From(Users).SQLSource()).
		Preload(Posts).
		All(context.Background()).Scan(&[]preUser{})
	if err == nil {
		t.Fatal("Preload without a From table should defer an error")
	}
}

// --- relation-driven UPDATE/DELETE ---

func TestUpdateJoinRel(t *testing.T) {
	db := newDB()
	sqlText, _, err := db.Update(Posts).Set(Posts.Title.Set("x")).JoinRel(PostsUser).
		Where(Users.Email.Eq("a@x")).ToSQL(sql.PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	if !contains(sqlText, "UPDATE") || !contains(sqlText, "FROM") {
		t.Fatalf("UpdateJoinRel SQL = %q", sqlText)
	}
}

func TestDeleteUsingRel(t *testing.T) {
	db := newDB()
	sqlText, _, err := db.Delete(Posts).UsingRel(PostsUser).
		Where(Users.Email.Eq("a@x")).ToSQL(sql.PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	if !contains(sqlText, "DELETE") || !contains(sqlText, "USING") {
		t.Fatalf("DeleteUsingRel SQL = %q", sqlText)
	}
}
