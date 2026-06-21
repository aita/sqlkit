package sqlkit_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
	"github.com/aita/sqlkit/sql"
)

func TestDeleteExecThroughDatabase(t *testing.T) {
	fake := &testutil.Fake{}
	db := sqlkit.WrapDB(fake.DB())

	if _, err := db.Delete(Users).Where(Users.ID.Eq(1)).Exec(context.Background()); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	execs := fake.Execs()
	if len(execs) != 1 || !strings.HasPrefix(execs[0].SQL, "DELETE FROM") {
		t.Fatalf("execs = %#v, want one DELETE", execs)
	}
}

func TestDeleteFromDB(t *testing.T) {
	db := sqlkit.WrapDB(usersFake().DB())
	query := db.Delete(Users).Where(sqlkit.Like(Users.Email, "%@spam.com"))
	assertToSQL(t, query,
		`DELETE FROM "public"."users" WHERE ("public"."users"."email" LIKE $1)`,
		[]any{"%@spam.com"})
}

// TestDeleteWithCTE covers WithCTE: a leading WITH clause referenced from a WHERE
// subquery.
func TestDeleteWithCTE(t *testing.T) {
	active := sqlkit.Select(Posts.UserID).From(Posts).CTE("active")
	query := sqlkit.Delete(Users).WithCTE(active).
		Where(Users.ID.InQuery(sqlkit.Select(sql.Col("user_id")).From(sql.Table{Name: sql.NewIdent("active")})))
	assertToSQL(t, query,
		`WITH "active" AS (SELECT "public"."posts"."user_id" FROM "public"."posts") DELETE FROM "public"."users" WHERE ("public"."users"."id" IN (SELECT "user_id" FROM "active"))`,
		nil)
}

// TestDeleteLeftJoin covers LeftJoin, the MySQL inline LEFT JOIN form of a
// multi-table DELETE (PostgreSQL has no inline LEFT JOIN for DELETE).
func TestDeleteLeftJoin(t *testing.T) {
	query := sqlkit.Delete(Users).
		LeftJoin(Posts, Users.ID.EqExpr(Posts.UserID)).
		Where(Posts.ID.IsNull())
	sqlText, _, err := query.ToSQL(sql.MySQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	want := "DELETE `public`.`users` FROM `public`.`users` LEFT JOIN `public`.`posts` ON (`public`.`users`.`id` = `public`.`posts`.`user_id`) WHERE (`public`.`posts`.`id` IS NULL)"
	if sqlText != want {
		t.Fatalf("ToSQL() = %q,\nwant %q", sqlText, want)
	}
}
