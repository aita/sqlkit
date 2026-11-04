package sqlkit_test

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
)

// scannerPost has a sql.Scanner primary key, which accepts NULL as its zero
// value, and a column whose Scanner rejects NULL outright.
type scannerPost struct {
	ID     stdsql.NullInt64 `sqlkit:"id"`
	UserID int64            `sqlkit:"user_id"`
	Title  strictText
}

type scannerUser struct {
	ID    int64 `sqlkit:"id"`
	Name  *string
	Posts []scannerPost
}

// strictText is a Scanner that refuses NULL, like many decimal/UUID types.
type strictText struct{ s string }

func (t *strictText) Scan(src any) error {
	switch v := src.(type) {
	case string:
		t.s = v
	case []byte:
		t.s = string(v)
	default:
		return fmt.Errorf("strictText: cannot scan %T", src)
	}
	return nil
}

// A parent with no LEFT-JOINed child must not get a phantom zero-valued child
// just because the child's key type is a sql.Scanner that accepts NULL, and a
// child column whose Scanner rejects NULL must not fail the whole scan.
func TestFoldScannerKeyNoPhantomChild(t *testing.T) {
	db := sqlkit.WrapDB(usersFoldPostsFake().DB())
	var users []scannerUser
	if err := db.Select().From(Users).FoldRel(UsersPosts).All(context.Background()).Scan(&users); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(users) != 3 {
		t.Fatalf("got %d users, want 3", len(users))
	}
	if n := len(users[2].Posts); n != 0 {
		t.Fatalf("Carol (no posts) got %d phantom post(s): %#v", n, users[2].Posts)
	}
	if len(users[0].Posts) != 2 || !users[0].Posts[0].ID.Valid || users[0].Posts[0].ID.Int64 != 10 || users[0].Posts[0].Title.s != "a" {
		t.Fatalf("Alice posts = %#v", users[0].Posts)
	}
}

// Terminals that limit or count rows would act on the joined rows, not the
// parents, so they are rejected on a folded query.
func TestFoldRejectsRowLimitingTerminals(t *testing.T) {
	db := sqlkit.WrapDB(usersFoldPostsFake().DB())
	ctx := context.Background()
	q := db.Select().From(Users).FoldRel(UsersPosts)
	var users []foldUser
	var u foldUser
	checks := map[string]error{
		"First":     q.First(ctx).Scan(&u),
		"One":       q.One(ctx).Scan(&u),
		"Get":       q.Get(ctx, int64(1)).Scan(&u),
		"Page":      q.Page(ctx, 10, 0).Scan(&users),
		"All+Limit": q.Limit(10).All(ctx).Scan(&users),
	}
	_, countErr := q.Count(ctx)
	checks["Count"] = countErr
	_, pageErr := sqlkit.NewPaginator(sqlkit.KeyAsc(Users.ID)).Limit(2).Paginate(ctx, q, &users, sqlkit.Start())
	checks["Paginate"] = pageErr
	for name, err := range checks {
		if err == nil || !strings.Contains(err.Error(), "FoldRel/Fold") {
			t.Errorf("%s on a folded query: err = %v, want a FoldRel/Fold rejection", name, err)
		}
	}
}

// A folded query never splits a large IN across statements: the chunks would
// be scanned flat instead of folded.
func TestFoldDoesNotSplitIn(t *testing.T) {
	fake := usersFoldPostsFake()
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithAutoChunk(true))
	ids := make([]int64, 100)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	var users []foldUser
	err := db.Select().From(Users).Where(Users.ID.In(ids...)).AutoChunk(sqlkit.ChunkSize(10)).
		FoldRel(UsersPosts).All(context.Background()).Scan(&users)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if n := len(fake.Queries()); n != 1 {
		t.Fatalf("queries = %d, want 1 (no split for a folded query)", n)
	}
	if len(users) != 3 || titles(users[0].Posts) != "a,c" {
		t.Fatalf("folded result = %#v", users)
	}
}
