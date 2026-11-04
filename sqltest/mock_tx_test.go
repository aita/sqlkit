package sqltest_test

import (
	"errors"
	"testing"

	"github.com/aita/sqlkit/sqltest"
)

func TestMockBeginCommit(t *testing.T) {
	db, mock := sqltest.NewDB(t)
	mock.ExpectBegin().Required()
	mock.ExpectCommit().Required()

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	// Verify at cleanup: both Required calls happened, so no failure.
}

func TestMockBeginError(t *testing.T) {
	db, mock := sqltest.NewDB(t)
	boom := errors.New("no begin")
	mock.ExpectBegin().ReturnError(boom)

	if _, err := db.Begin(); !errors.Is(err, boom) {
		t.Fatalf("begin err = %v, want boom", err)
	}
}

func TestMockCommitError(t *testing.T) {
	db, mock := sqltest.NewDB(t)
	boom := errors.New("commit failed")
	mock.ExpectCommit().ReturnError(boom)

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := tx.Commit(); !errors.Is(err, boom) {
		t.Fatalf("commit err = %v, want boom", err)
	}
}

func TestMockRollback(t *testing.T) {
	db, mock := sqltest.NewDB(t)
	mock.ExpectRollback().Required()

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
}

func TestMockTxNoExpectationsStillWorks(t *testing.T) {
	db, _ := sqltest.NewDB(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func TestMockPingError(t *testing.T) {
	db, mock := sqltest.NewDB(t)
	boom := errors.New("db down")
	mock.ExpectPing().ReturnError(boom)

	if err := db.Ping(); !errors.Is(err, boom) {
		t.Fatalf("ping err = %v, want boom", err)
	}
}

func TestRowSetFromCSVString(t *testing.T) {
	db, mock := sqltest.NewDB(t)
	mock.ExpectQuery(sqltest.Contains("FROM users")).
		Return(sqltest.NewRows("id", "name").FromCSVString("1,alice\n2,bob"))

	rows, err := db.Query("SELECT id, name FROM users")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	type user struct {
		id   int
		name string
	}
	var got []user
	for rows.Next() {
		var u user
		if err := rows.Scan(&u.id, &u.name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, u)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	want := []user{{1, "alice"}, {2, "bob"}}
	if len(got) != len(want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d = %v, want %v", i, got[i], want[i])
		}
	}
}

// A CSV record whose field count differs from the RowSet's columns is a test
// setup error, not a row padded with NULLs.
func TestRowSetFromCSVStringRejectsShortRecord(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("FromCSVString with a short record did not panic")
		}
	}()
	sqltest.NewRows("id", "name").FromCSVString("1,alice\n2")
}
