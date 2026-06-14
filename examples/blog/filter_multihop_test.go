package main

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/examples/blog/blogdb"
	"github.com/aita/sqlkit/sql"
)

// TestMultiHopRelationFilter exercises a relation filter nested inside another
// relation filter against the generated bindings (users → posts → users): the
// authors of a post written by a given user. Each level must emit a correlated
// EXISTS, which only works when a relation's Target() exposes the target's own
// relations — i.e. returns the table binding, not the bare columns value.
func TestMultiHopRelationFilter(t *testing.T) {
	q := sqlkit.Select().From(blogdb.Users).Filter(blogdb.UsersFilter{
		Posts: &blogdb.PostsFilter{
			User: &blogdb.UsersFilter{Email: sqlkit.Eq("a@example.com")},
		},
	})

	got, _, err := q.ToSQL(sql.PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	if n := strings.Count(got, "EXISTS (SELECT 1"); n != 2 {
		t.Fatalf("want two nested EXISTS, got %d:\n%s", n, got)
	}
	if !strings.Contains(got, `"public"."users"."email" = $1`) {
		t.Fatalf("missing innermost email predicate:\n%s", got)
	}
}
