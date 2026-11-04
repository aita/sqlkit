package sqlkit_test

import (
	"context"
	"database/sql/driver"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
	"github.com/aita/sqlkit/sql"
)

// PostMetaTable has no primary key, so a to-one fold onto it can't detect a
// missing LEFT JOIN match through a NULL key.
type PostMetaTable struct {
	info   sqlkit.TableInfo
	PostID sqlkit.Column[int64]
	Note   sqlkit.Column[string]
}

var PostMeta = func() PostMetaTable {
	info := sqlkit.TableInfo{Schema: "public", Name: "post_meta"}
	return PostMetaTable{
		info:   info,
		PostID: sqlkit.NewColumn[int64](info, "post_id"),
		Note:   sqlkit.NewColumn[string](info, "note"),
	}
}()

func (t PostMetaTable) Info() sqlkit.TableInfo             { return t.info }
func (t PostMetaTable) SQLSource() sqlkit.SourceExpression { return t.info.SQLSource() }
func (t PostMetaTable) Columns() []sqlkit.AnyColumn {
	return []sqlkit.AnyColumn{t.PostID, t.Note}
}
func (t PostMetaTable) PrimaryKey() []sqlkit.AnyColumn { return nil }
func (t PostMetaTable) Defaulted() []sqlkit.AnyColumn  { return nil }

type postsMetaRel struct{ PostMetaTable }

func (r postsMetaRel) Target() sqlkit.Table { return r.PostMetaTable }
func (r postsMetaRel) JoinOn() sqlkit.Predicate {
	return sqlkit.Cond(sql.Eq(r.PostID, Posts.ID))
}
func (r postsMetaRel) Cardinality() sqlkit.Cardinality { return sqlkit.ToOne }
func (r postsMetaRel) Name() string                    { return "Meta" }
func (r postsMetaRel) Keys() (target, base []string)   { return []string{"post_id"}, []string{"id"} }

var PostsMeta = postsMetaRel{PostMeta}

type pmMeta struct {
	PostID int64 `sqlkit:"post_id"`
	Note   string
}

type pmPost struct {
	ID     int64 `sqlkit:"id"`
	UserID int64 `sqlkit:"user_id"`
	Title  string
	Meta   *pmMeta
}

// A post with no post_meta row (the LEFT JOIN yields NULLs) must keep a nil
// Meta, not a pointer to a zero row that never existed.
func TestFoldRelToOneWithoutPrimaryKey(t *testing.T) {
	fake := &testutil.Fake{
		QueryFunc: func(string, []any) (testutil.Result, error) {
			return testutil.Result{
				Columns: []string{"id", "user_id", "title", "post_id", "note"},
				Rows: [][]driver.Value{
					{int64(10), int64(1), "a", int64(10), "n"},
					{int64(11), int64(1), "b", nil, nil}, // no meta row
				},
			}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	var posts []pmPost
	if err := db.Select().From(Posts).FoldRel(PostsMeta).
		All(context.Background()).Scan(&posts); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(posts) != 2 {
		t.Fatalf("got %d posts, want 2", len(posts))
	}
	if posts[0].Meta == nil || posts[0].Meta.PostID != 10 || posts[0].Meta.Note != "n" {
		t.Fatalf("posts[0].Meta = %#v", posts[0].Meta)
	}
	if posts[1].Meta != nil {
		t.Fatalf("posts[1].Meta = %#v, want nil (no matching row)", posts[1].Meta)
	}
}
