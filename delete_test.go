package sqlkit_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
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
