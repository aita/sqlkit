package sqlkit_test

import (
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/sql"
)

// TestCardinalityString covers Cardinality.String over both kinds plus an
// out-of-range value, which renders the diagnostic placeholder.
func TestCardinalityString(t *testing.T) {
	cases := map[sqlkit.Cardinality]string{
		sqlkit.ToOne:           "ToOne",
		sqlkit.ToMany:          "ToMany",
		sqlkit.Cardinality(99): "Cardinality(?)",
	}
	for c, want := range cases {
		if got := c.String(); got != want {
			t.Fatalf("Cardinality(%d).String() = %q, want %q", c, got, want)
		}
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
