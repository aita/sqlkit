package sqlkit_test

import (
	"context"
	"database/sql/driver"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
	"github.com/aita/sqlkit/sql"
)

// foldUser folds its has-many Posts relation in a single joined query.
type foldUser struct {
	ID    int64 `sqlkit:"id"`
	Name  *string
	Posts []Post
}

// foldAuthor and foldPost exercise a to-one fold (posts → users).
type foldAuthor struct {
	ID   int64 `sqlkit:"id"`
	Name *string
}

type foldPost struct {
	ID     int64 `sqlkit:"id"`
	UserID int64 `sqlkit:"user_id"`
	Title  string
	User   *foldAuthor
}

// usersFoldPostsFake serves one LEFT JOIN users→posts result: Alice has two
// posts, Bob one, Carol none (the joined post columns are NULL).
func usersFoldPostsFake() *testutil.Fake {
	return &testutil.Fake{
		QueryFunc: func(string, []any) (testutil.Result, error) {
			return testutil.Result{
				Columns: []string{"id", "email", "name", "id", "user_id", "title"},
				Rows: [][]driver.Value{
					{int64(1), "a@x", "Alice", int64(10), int64(1), "a"},
					{int64(1), "a@x", "Alice", int64(12), int64(1), "c"},
					{int64(2), "b@x", "Bob", int64(11), int64(2), "b"},
					{int64(3), "c@x", "Carol", nil, nil, nil},
				},
			}, nil
		},
	}
}

func TestFoldRelToMany(t *testing.T) {
	db := sqlkit.WrapDB(usersFoldPostsFake().DB())
	ctx := context.Background()

	var users []foldUser
	if err := db.Select().From(Users).FoldRel(UsersPosts).
		All(ctx).Scan(&users); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	if len(users) != 3 {
		t.Fatalf("got %d users, want 3: %#v", len(users), users)
	}
	if users[0].ID != 1 || users[0].Name == nil || *users[0].Name != "Alice" {
		t.Fatalf("users[0] = %#v", users[0])
	}
	if got := titles(users[0].Posts); got != "a,c" {
		t.Fatalf("Alice posts = %q, want \"a,c\"", got)
	}
	if got := titles(users[1].Posts); got != "b" {
		t.Fatalf("Bob posts = %q, want \"b\"", got)
	}
	if len(users[2].Posts) != 0 {
		t.Fatalf("Carol posts = %#v, want none", users[2].Posts)
	}
	// The folded post rows carry their own columns.
	if users[0].Posts[1].ID != 12 || users[0].Posts[1].UserID != 1 || users[0].Posts[1].Title != "c" {
		t.Fatalf("Alice second post = %#v", users[0].Posts[1])
	}
}

func TestFoldResolvesRelationFromTarget(t *testing.T) {
	db := sqlkit.WrapDB(usersFoldPostsFake().DB())
	ctx := context.Background()

	var users []foldUser
	// Fold(Posts) resolves the users→posts relation from the target table.
	if err := db.Select().From(Users).Fold(Posts).All(ctx).Scan(&users); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(users) != 3 || titles(users[0].Posts) != "a,c" {
		t.Fatalf("Fold(Posts) = %#v", users)
	}
}

func TestFoldRelToOne(t *testing.T) {
	fake := &testutil.Fake{
		QueryFunc: func(string, []any) (testutil.Result, error) {
			return testutil.Result{
				Columns: []string{"id", "user_id", "title", "id", "email", "name"},
				Rows: [][]driver.Value{
					{int64(10), int64(1), "a", int64(1), "a@x", "Alice"},
					{int64(11), int64(2), "b", int64(2), "b@x", "Bob"},
					{int64(13), int64(99), "z", nil, nil, nil}, // no matching user
				},
			}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())
	ctx := context.Background()

	var posts []foldPost
	if err := db.Select().From(Posts).FoldRel(PostsUser).
		All(ctx).Scan(&posts); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(posts) != 3 {
		t.Fatalf("got %d posts, want 3", len(posts))
	}
	if posts[0].User == nil || posts[0].User.ID != 1 || posts[0].User.Name == nil || *posts[0].User.Name != "Alice" {
		t.Fatalf("posts[0].User = %#v", posts[0].User)
	}
	if posts[1].User == nil || posts[1].User.ID != 2 {
		t.Fatalf("posts[1].User = %#v", posts[1].User)
	}
	if posts[2].User != nil {
		t.Fatalf("posts[2].User = %#v, want nil (no matching user)", posts[2].User)
	}
}

func TestFoldRelRejectsExplicitColumns(t *testing.T) {
	db := newDB()
	var users []foldUser
	err := db.Select(Users.ID).From(Users).FoldRel(UsersPosts).
		All(context.Background()).Scan(&users)
	if err == nil || !contains(err.Error(), "no explicit columns") {
		t.Fatalf("error = %v, want explicit-columns rejection", err)
	}
}

// CommentsTable + PostsComments give a third hop (posts → comments) for nested
// fold tests.
type CommentsTable struct {
	info   sqlkit.TableInfo
	ID     sqlkit.Column[int64]
	PostID sqlkit.Column[int64]
	Body   sqlkit.Column[string]
}

var Comments = func() CommentsTable {
	info := sqlkit.TableInfo{Schema: "public", Name: "comments"}
	return CommentsTable{
		info:   info,
		ID:     sqlkit.NewColumn[int64](info, "id"),
		PostID: sqlkit.NewColumn[int64](info, "post_id"),
		Body:   sqlkit.NewColumn[string](info, "body"),
	}
}()

func (t CommentsTable) Info() sqlkit.TableInfo             { return t.info }
func (t CommentsTable) SQLSource() sqlkit.SourceExpression { return t.info.SQLSource() }
func (t CommentsTable) Columns() []sqlkit.AnyColumn {
	return []sqlkit.AnyColumn{t.ID, t.PostID, t.Body}
}
func (t CommentsTable) PrimaryKey() []sqlkit.AnyColumn { return []sqlkit.AnyColumn{t.ID} }
func (t CommentsTable) Defaulted() []sqlkit.AnyColumn  { return []sqlkit.AnyColumn{t.ID} }

// postsCommentsRel is the has-many from posts to comments (comments.post_id =
// posts.id).
type postsCommentsRel struct{ CommentsTable }

func (r postsCommentsRel) Target() sqlkit.Table { return r.CommentsTable }
func (r postsCommentsRel) JoinOn() sqlkit.Predicate {
	return sqlkit.Cond(sql.Eq(r.PostID, Posts.ID))
}
func (r postsCommentsRel) Cardinality() sqlkit.Cardinality { return sqlkit.ToMany }
func (r postsCommentsRel) Name() string                    { return "Comments" }
func (r postsCommentsRel) Keys() (target, base []string)   { return []string{"post_id"}, []string{"id"} }

var PostsComments = postsCommentsRel{Comments}

var _ sqlkit.Relation = PostsComments

// fcUser/fcPost/fcComment exercise a nested fold (users → posts → comments).
type fcUser struct {
	ID    int64 `sqlkit:"id"`
	Posts []fcPost
}

type fcPost struct {
	ID       int64 `sqlkit:"id"`
	UserID   int64 `sqlkit:"user_id"`
	Comments []fcComment
}

type fcComment struct {
	ID     int64 `sqlkit:"id"`
	PostID int64 `sqlkit:"post_id"`
	Body   string
}

// usersPostsCommentsFake serves one users→posts→comments LEFT JOIN result: user
// 1 has post 10 (comments x,y) and post 12 (no comments); user 2 has no posts.
func usersPostsCommentsFake() *testutil.Fake {
	return &testutil.Fake{
		QueryFunc: func(string, []any) (testutil.Result, error) {
			return testutil.Result{
				Columns: []string{"id", "email", "name", "id", "user_id", "title", "id", "post_id", "body"},
				Rows: [][]driver.Value{
					{int64(1), "a", "A", int64(10), int64(1), "t10", int64(100), int64(10), "x"},
					{int64(1), "a", "A", int64(10), int64(1), "t10", int64(101), int64(10), "y"},
					{int64(1), "a", "A", int64(12), int64(1), "t12", nil, nil, nil},
					{int64(2), "b", "B", nil, nil, nil, nil, nil, nil},
				},
			}, nil
		},
	}
}

func TestFoldNested(t *testing.T) {
	db := sqlkit.WrapDB(usersPostsCommentsFake().DB())
	ctx := context.Background()

	var users []fcUser
	if err := db.Select().From(Users).
		Fold(sqlkit.RelPath(Posts, Comments)).
		All(ctx).Scan(&users); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	if len(users) != 2 {
		t.Fatalf("got %d users, want 2: %#v", len(users), users)
	}
	if len(users[0].Posts) != 2 {
		t.Fatalf("user1 posts = %#v, want 2", users[0].Posts)
	}
	if bodies(users[0].Posts[0].Comments) != "x,y" {
		t.Fatalf("post10 comments = %q, want \"x,y\"", bodies(users[0].Posts[0].Comments))
	}
	if len(users[0].Posts[1].Comments) != 0 {
		t.Fatalf("post12 comments = %#v, want none", users[0].Posts[1].Comments)
	}
	if len(users[1].Posts) != 0 {
		t.Fatalf("user2 posts = %#v, want none", users[1].Posts)
	}
}

func TestFoldNestedSQLJoinsEachHop(t *testing.T) {
	s, _, err := newDB().Select().From(Users).
		Fold(sqlkit.RelPath(Posts, Comments)).
		ToSQL(pg)
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	if !contains(s, `LEFT JOIN "public"."posts"`) || !contains(s, `LEFT JOIN "public"."comments"`) {
		t.Fatalf("nested fold SQL missing a hop: %s", s)
	}
}

// saUser/saPost/saAuthor fold users → posts → author, where the author hop
// returns to the base users table and must be aliased.
type saUser struct {
	ID    int64 `sqlkit:"id"`
	Posts []saPost
}

type saPost struct {
	ID     int64 `sqlkit:"id"`
	UserID int64 `sqlkit:"user_id"`
	User   *saAuthor
}

type saAuthor struct {
	ID   int64 `sqlkit:"id"`
	Name *string
}

func TestFoldSelfReferenceAliasesJoin(t *testing.T) {
	fake := &testutil.Fake{
		QueryFunc: func(string, []any) (testutil.Result, error) {
			return testutil.Result{
				Columns: []string{"id", "email", "name", "id", "user_id", "title", "id", "email", "name"},
				Rows: [][]driver.Value{
					{int64(1), "a", "Alice", int64(10), int64(1), "t10", int64(1), "a", "Alice"},
					{int64(2), "b", "Bob", int64(11), int64(2), "t11", int64(2), "b", "Bob"},
				},
			}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	var users []saUser
	if err := db.Select().From(Users).
		Fold(sqlkit.RelPath(Posts, Users)).
		All(context.Background()).Scan(&users); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(users) != 2 || len(users[0].Posts) != 1 {
		t.Fatalf("users = %#v", users)
	}
	a := users[0].Posts[0].User
	if a == nil || a.ID != 1 || a.Name == nil || *a.Name != "Alice" {
		t.Fatalf("post author = %#v", a)
	}
	if users[1].Posts[0].User == nil || users[1].Posts[0].User.ID != 2 {
		t.Fatalf("user2 post author = %#v", users[1].Posts[0].User)
	}
}

func TestFoldSelfReferenceSQLAliasesTarget(t *testing.T) {
	s, _, err := newDB().Select().From(Users).
		Fold(sqlkit.RelPath(Posts, Users)).
		ToSQL(pg)
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	if !contains(s, `LEFT JOIN "public"."users" AS "User"`) {
		t.Fatalf("self-reference hop not aliased: %s", s)
	}
	if !contains(s, `"User"."id" = "public"."posts"."user_id"`) {
		t.Fatalf("aliased ON not rendered: %s", s)
	}
}

func bodies(comments []fcComment) string {
	out := ""
	for i, c := range comments {
		if i > 0 {
			out += ","
		}
		out += c.Body
	}
	return out
}

func titles(posts []Post) string {
	out := ""
	for i, p := range posts {
		if i > 0 {
			out += ","
		}
		out += p.Title
	}
	return out
}
