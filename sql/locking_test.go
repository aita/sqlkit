package sql_test

import (
	"strings"
	"testing"

	. "github.com/aita/sqlkit/sql"
)

func TestForUpdateAndShare(t *testing.T) {
	base := Select(Col("id")).From(Table{Name: "jobs"})

	assertSQL(t, base.ForUpdate(), `SELECT "id" FROM "jobs" FOR UPDATE`, nil)
	assertSQL(t, base.ForShare(), `SELECT "id" FROM "jobs" FOR SHARE`, nil)
	assertSQL(t, base.ForNoKeyUpdate(), `SELECT "id" FROM "jobs" FOR NO KEY UPDATE`, nil)
	assertSQL(t, base.ForKeyShare(), `SELECT "id" FROM "jobs" FOR KEY SHARE`, nil)
}

func TestLockWaitModifiers(t *testing.T) {
	base := Select(Col("id")).From(Table{Name: "jobs"})

	assertSQL(t, base.ForUpdate(SkipLocked()),
		`SELECT "id" FROM "jobs" FOR UPDATE SKIP LOCKED`, nil)
	assertSQL(t, base.ForUpdate(NoWait()),
		`SELECT "id" FROM "jobs" FOR UPDATE NOWAIT`, nil)
}

func TestLockOf(t *testing.T) {
	q := Select(Col("jobs.id")).From(Table{Name: "jobs"}).
		Join(Table{Name: "queues"}, Eq(Col("jobs.queue_id"), Col("queues.id"))).
		ForUpdate(LockOf("jobs"), SkipLocked())
	assertSQL(t, q,
		`SELECT "jobs"."id" FROM "jobs" JOIN "queues" ON ("jobs"."queue_id" = "queues"."id") FOR UPDATE OF "jobs" SKIP LOCKED`,
		nil)
}

func TestLockComesAfterLimitOffset(t *testing.T) {
	q := Select(Col("id")).From(Table{Name: "jobs"}).
		OrderBy(Asc(Col("id"))).Limit(10).Offset(5).ForUpdate(SkipLocked())
	assertSQL(t, q,
		`SELECT "id" FROM "jobs" ORDER BY "id" LIMIT 10 OFFSET 5 FOR UPDATE SKIP LOCKED`, nil)
}

func TestLockMySQL(t *testing.T) {
	assertMySQL(t, Select(Col("id")).From(Table{Name: "jobs"}).ForUpdate(SkipLocked()),
		"SELECT `id` FROM `jobs` FOR UPDATE SKIP LOCKED", nil)
	assertMySQL(t, Select(Col("id")).From(Table{Name: "jobs"}).ForShare(),
		"SELECT `id` FROM `jobs` FOR SHARE", nil)

	// MySQL has no NO KEY UPDATE / KEY SHARE.
	if _, _, err := Select(Col("id")).From(Table{Name: "jobs"}).ForKeyShare().ToSQL(MySQLDialect{}); err == nil ||
		!strings.Contains(err.Error(), "FOR UPDATE and FOR SHARE") {
		t.Fatalf("ToSQL(mysql) error = %v, want strength rejection", err)
	}
}

func TestLockRejectedOnSQLiteAndStandard(t *testing.T) {
	q := Select(Col("id")).From(Table{Name: "jobs"}).ForUpdate()
	for _, d := range []Dialect{SQLiteDialect{}, StandardSQLDialect{}} {
		if _, _, err := q.ToSQL(d); err == nil || !strings.Contains(err.Error(), "row-locking") {
			t.Fatalf("ToSQL(%s) error = %v, want row-locking rejection", d.Name(), err)
		}
	}
}
