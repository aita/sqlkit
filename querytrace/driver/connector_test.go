package driver_test

import (
	"context"
	stdsql "database/sql"
	stddriver "database/sql/driver"
	"testing"

	qtdriver "github.com/aita/sqlkit/querytrace/driver"
)

// TestConnectorDriverForwarded checks the wrapping connector exposes the
// underlying driver unchanged, so database/sql machinery that reaches for
// Connector.Driver() (e.g. DB.Driver) sees the real driver.
func TestConnectorDriverForwarded(t *testing.T) {
	wrapped := qtdriver.WrapConnector(fakeConnector{rowCount: 1}, testConfig())
	db := stdsql.OpenDB(wrapped)
	t.Cleanup(func() { _ = db.Close() })

	if got := db.Driver(); got != (fakeDriver{}) {
		t.Errorf("db.Driver() = %T %v, want fakeDriver{}", got, got)
	}
}

// errConnector returns a connection-open error so the wrapper's error
// propagation in Connect is exercised.
type errConnector struct{ err error }

func (c errConnector) Connect(context.Context) (stddriver.Conn, error) { return nil, c.err }
func (c errConnector) Driver() stddriver.Driver                        { return fakeDriver{} }

// TestConnectorConnectError verifies a Connect failure from the underlying
// connector propagates unchanged (and no connection is returned).
func TestConnectorConnectError(t *testing.T) {
	want := errSentinel("connect boom")
	db := stdsql.OpenDB(qtdriver.WrapConnector(errConnector{err: want}, testConfig()))
	t.Cleanup(func() { _ = db.Close() })

	_, err := db.Conn(context.Background())
	if err != want {
		t.Fatalf("Conn error = %v, want %v", err, want)
	}
}

type errSentinel string

func (e errSentinel) Error() string { return string(e) }
