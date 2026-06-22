package driver_test

import (
	"context"
	stdsql "database/sql"
	stddriver "database/sql/driver"
	"testing"

	qtdriver "github.com/aita/sqlkit/querytrace/driver"
)

type resetConn struct {
	resets   int
	isValids int
	valid    bool
}

var (
	_ stddriver.Conn               = (*resetConn)(nil)
	_ stddriver.QueryerContext     = (*resetConn)(nil)
	_ stddriver.ConnPrepareContext = (*resetConn)(nil)
	_ stddriver.SessionResetter    = (*resetConn)(nil)
	_ stddriver.Validator          = (*resetConn)(nil)
)

func (c *resetConn) ResetSession(context.Context) error { c.resets++; return nil }
func (c *resetConn) IsValid() bool                      { c.isValids++; return c.valid }

func (c *resetConn) QueryContext(context.Context, string, []stddriver.NamedValue) (stddriver.Rows, error) {
	return &fakeRows{remaining: 1}, nil
}
func (c *resetConn) PrepareContext(context.Context, string) (stddriver.Stmt, error) {
	return &fakeStmt{rowCount: 1}, nil
}
func (c *resetConn) Prepare(string) (stddriver.Stmt, error) { return &fakeStmt{rowCount: 1}, nil }
func (c *resetConn) Close() error                           { return nil }
func (c *resetConn) Begin() (stddriver.Tx, error)           { return fakeTx{}, nil }

// TestConnResetSessionAndIsValidForwarded drives the real database/sql over the
// wrapped connector with a single connection pinned by SetMaxOpenConns(1) and a
// loop of queries. database/sql calls ResetSession when it re-checks out a
// connection it had returned to the pool, and IsValid when it puts a connection
// back; both reach the underlying conn only if the wrapper forwards them.
func TestConnResetSessionAndIsValidForwarded(t *testing.T) {
	under := &resetConn{valid: true}
	connector := connFactory(func() stddriver.Conn { return under })
	db := stdsql.OpenDB(qtdriver.WrapConnector(connector, testConfig()))
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	ctx := context.Background()
	for i := 0; i < 5; i++ {
		rows, err := db.QueryContext(ctx, "SELECT * FROM users")
		if err != nil {
			t.Fatalf("query %d: %v", i, err)
		}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				t.Fatalf("scan: %v", err)
			}
		}
		if err := rows.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
	}

	if under.resets == 0 {
		t.Error("ResetSession was never forwarded to the underlying connection")
	}
	if under.isValids == 0 {
		t.Error("IsValid was never forwarded to the underlying connection")
	}
}

// TestConnIsValidEvictsBrokenConnection checks that a connection reporting
// invalid via the forwarded Validator is dropped from the pool: database/sql
// validates a pooled connection on checkout and discards it when IsValid is
// false, opening a fresh one. A non-forwarding wrapper would hide that and reuse
// the broken connection.
func TestConnIsValidEvictsBrokenConnection(t *testing.T) {
	conns := 0
	connector := connFactory(func() stddriver.Conn {
		conns++
		// Every handed-out connection reports itself broken, so each pooled
		// connection must be discarded and a new one opened on the next query.
		return &resetConn{valid: false}
	})
	db := stdsql.OpenDB(qtdriver.WrapConnector(connector, testConfig()))
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		rows, err := db.QueryContext(ctx, "SELECT * FROM users")
		if err != nil {
			t.Fatalf("query %d: %v", i, err)
		}
		_ = rows.Close()
	}

	if conns < 2 {
		t.Errorf("opened %d connections; a forwarded IsValid()==false should evict and reopen", conns)
	}
}

// connFactory builds a connector whose Connect calls make to mint each new
// underlying connection.
type connFactory func() stddriver.Conn

func (f connFactory) Connect(context.Context) (stddriver.Conn, error) { return f(), nil }
func (f connFactory) Driver() stddriver.Driver                        { return fakeDriver{} }
