package dbtest

import (
	"context"
	"testing"

	"github.com/aita/sqlkit"
)

// Fixture is a named, ordered set of rows spanning one or more tables. Declare
// it once and Load it into a database in any test that needs the same starting
// data:
//
//	var blog = dbtest.NewFixture("blog").
//		Add(blogdb.Users, userRow{Email: "alice@example.com"}, userRow{Email: "bob@example.com"}).
//		Add(blogdb.Posts, postRow{Title: "Hello"})
//
//	blog.Load(t, db)
//
// Groups load in declaration order, so add parent tables before the children
// that reference them — foreign-key ordering is the caller's responsibility,
// the same as hand-written seed code. Fixture is for static rows whose values
// are known up front; when later rows must reference a generated key (e.g. a
// post needs its author's id), capture it with InsertID in a plain seed
// function instead.
type Fixture struct {
	name   string
	groups []fixtureGroup
}

type fixtureGroup struct {
	table sqlkit.Table
	rows  []any
}

// NewFixture creates an empty fixture. name appears in failure messages.
func NewFixture(name string) *Fixture {
	return &Fixture{name: name}
}

// Add appends a group of rows for table, preserving call order. It returns the
// fixture so calls chain. Each row follows the same shapes Insert accepts
// (struct, pointer to struct, or slice of either).
func (f *Fixture) Add(table sqlkit.Table, rows ...any) *Fixture {
	f.groups = append(f.groups, fixtureGroup{table: table, rows: rows})
	return f
}

// Load inserts every group in declaration order, failing the test on the first
// error. ex is any sqlkit.Executor, so a fixture loads through a *sqlkit.Database
// or within a *sqlkit.Session transaction alike.
func (f *Fixture) Load(t *testing.T, ex sqlkit.Executor) {
	t.Helper()
	for _, group := range f.groups {
		if len(group.rows) == 0 {
			continue
		}
		if _, err := ex.Insert(group.table).Values(group.rows...).Exec(context.Background()); err != nil {
			t.Fatalf("dbtest: load fixture %q into %s: %v", f.name, group.table.Info().QualifiedName(), err)
		}
	}
}
