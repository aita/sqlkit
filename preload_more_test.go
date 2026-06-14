package sqlkit_test

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/sqltest"
)

// taggedUser resolves its preload destination by a `preload:"Posts"` tag rather
// than by a field named after the relation, so the field can be named anything.
type taggedUser struct {
	ID       int64     `sqlkit:"id"`
	Authored []prePost `preload:"Posts"`
}

// TestPreloadResolvesFieldByTag covers relationField's tag branch: the relation
// rows land in the tagged field even though it is not named after the relation.
func TestPreloadResolvesFieldByTag(t *testing.T) {
	fake := &sqltest.Fake{
		QueryFunc: func(s string, _ []any) (sqltest.Result, error) {
			if contains(s, `"public"."posts"`) {
				return sqltest.Result{Columns: []string{"id", "user_id", "title"}, Rows: [][]driver.Value{
					{int64(10), int64(1), "a"},
				}}, nil
			}
			return sqltest.Result{Columns: []string{"id"}, Rows: [][]driver.Value{{int64(1)}}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	var users []taggedUser
	if err := db.Select(Users.ID).From(Users).Preload(Posts).All(context.Background()).Scan(&users); err != nil {
		t.Fatalf("Preload chain error = %v", err)
	}
	if len(users) != 1 || len(users[0].Authored) != 1 || users[0].Authored[0].Title != "a" {
		t.Fatalf("tagged field not populated: %+v", users)
	}
}

// toManyScalarUser declares a to-many relation field that is not a slice, which
// the preload must reject.
type toManyScalarUser struct {
	ID    int64   `sqlkit:"id"`
	Posts prePost // not a slice, though Users->Posts is to-many
}

// TestPreloadToManyNonSliceFieldErrors covers the guard that a to-many relation
// requires a slice destination field.
func TestPreloadToManyNonSliceFieldErrors(t *testing.T) {
	fake := &sqltest.Fake{QueryFunc: func(string, []any) (sqltest.Result, error) {
		return sqltest.Result{Columns: []string{"id"}, Rows: [][]driver.Value{{int64(1)}}}, nil
	}}
	db := sqlkit.WrapDB(fake.DB())

	var users []toManyScalarUser
	err := db.Select(Users.ID).From(Users).Preload(Posts).All(context.Background()).Scan(&users)
	if err == nil || !strings.Contains(err.Error(), "must be a slice") {
		t.Fatalf("Preload error = %v, want a 'must be a slice' rejection", err)
	}
}

// nullableKeyUser has a nullable (pointer) primary-key field, exercising
// scalarValue's pointer-deref and nil-skip paths during preload.
type nullableKeyUser struct {
	ID    *int64 `sqlkit:"id"`
	Posts []prePost
}

// TestPreloadSkipsNullParentKey covers scalarValue: a parent whose key is nil is
// skipped, while parents with a non-nil key are still loaded.
func TestPreloadSkipsNullParentKey(t *testing.T) {
	var postsQueried int
	fake := &sqltest.Fake{
		QueryFunc: func(s string, _ []any) (sqltest.Result, error) {
			if contains(s, `"public"."posts"`) {
				postsQueried++
				return sqltest.Result{Columns: []string{"id", "user_id", "title"}, Rows: [][]driver.Value{
					{int64(10), int64(1), "a"},
				}}, nil
			}
			return sqltest.Result{Columns: []string{"id"}, Rows: [][]driver.Value{
				{int64(1)}, {nil},
			}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	var users []nullableKeyUser
	if err := db.Select(Users.ID).From(Users).Preload(Posts).All(context.Background()).Scan(&users); err != nil {
		t.Fatalf("Preload chain error = %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("scanned users = %d, want 2", len(users))
	}
	// First user (id=1) got its post; the nil-key user got none.
	if len(users[0].Posts) != 1 || users[0].Posts[0].Title != "a" {
		t.Fatalf("user 1 posts = %+v", users[0].Posts)
	}
	if len(users[1].Posts) != 0 {
		t.Fatalf("nil-key user should have no posts, got %+v", users[1].Posts)
	}
}

// TestPreloadThroughSessionUsesItsChunkSize covers Session.preloadChunkSize: a
// session inherits the database's WithPreloadBatchSize, so a preload over more
// distinct parent keys than the batch size runs several follow-up IN queries.
func TestPreloadThroughSessionUsesItsChunkSize(t *testing.T) {
	var postsQueries int
	fake := &sqltest.Fake{
		QueryFunc: func(s string, _ []any) (sqltest.Result, error) {
			if contains(s, `"public"."posts"`) {
				postsQueries++
				return sqltest.Result{Columns: []string{"id", "user_id", "title"}}, nil
			}
			return sqltest.Result{Columns: []string{"id"}, Rows: [][]driver.Value{
				{int64(1)}, {int64(2)}, {int64(3)},
			}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithPreloadBatchSize(2))
	session := db.NewSession()
	defer session.Close()

	var users []preUser
	if err := session.Select(Users.ID).From(Users).Preload(Posts).All(context.Background()).Scan(&users); err != nil {
		t.Fatalf("Preload chain error = %v", err)
	}
	// 3 distinct parent keys, batch size 2 => ceil(3/2) = 2 follow-up queries.
	if postsQueries != 2 {
		t.Fatalf("posts follow-up queries = %d, want 2 (chunked by the session's preload batch size)", postsQueries)
	}
}
