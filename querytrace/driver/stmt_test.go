package driver_test

import (
	"context"
	stdsql "database/sql"
	stddriver "database/sql/driver"
	"testing"

	qtdriver "github.com/aita/sqlkit/querytrace/driver"
)

// The connections used here deliberately implement neither QueryerContext nor
// ExecerContext, so database/sql routes every Exec through a prepared statement —
// the path where it consults the statement's ColumnConverter for argument
// conversion (database/sql's driverArgsConnLocked looks the converter up on the
// statement object it holds, which is our wrapper).

// ccStmt is a statement that implements driver.ColumnConverter, replacing any
// incoming value with the sentinel so the test can observe whether database/sql
// actually consulted the converter.
type ccStmt struct {
	converterCalls int
	lastValue      stddriver.Value
}

const ccSentinel = "converted-by-column-converter"

var (
	_ stddriver.Stmt            = (*ccStmt)(nil)
	_ stddriver.ColumnConverter = (*ccStmt)(nil) //nolint:staticcheck // exercising the legacy interface on purpose
)

func (s *ccStmt) Close() error  { return nil }
func (s *ccStmt) NumInput() int { return 1 }
func (s *ccStmt) ColumnConverter(int) stddriver.ValueConverter {
	s.converterCalls++
	return ccValueConverter{s}
}
func (s *ccStmt) Exec(args []stddriver.Value) (stddriver.Result, error) {
	if len(args) == 1 {
		s.lastValue = args[0]
	}
	return fakeResult{}, nil
}
func (s *ccStmt) Query([]stddriver.Value) (stddriver.Rows, error) {
	return &fakeRows{remaining: 0}, nil
}

type ccValueConverter struct{ s *ccStmt }

func (c ccValueConverter) ConvertValue(any) (stddriver.Value, error) { return ccSentinel, nil }

// TestStmtColumnConverterConsultedWhenUnderlyingProvidesOne verifies the wrapper
// no longer hides an underlying statement's ColumnConverter: with the converter
// present, database/sql must route the Exec argument through it (replacing it
// with the sentinel) rather than applying its default conversion.
func TestStmtColumnConverterConsultedWhenUnderlyingProvidesOne(t *testing.T) {
	// Capture the single underlying statement instance via a shared conn so the
	// test can inspect it after the Exec.
	under := &ccStmt{}
	connector := connFactory(func() stddriver.Conn { return &captureConn{stmt: under} })
	db := stdsql.OpenDB(qtdriver.WrapConnector(connector, testConfig()))
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.ExecContext(context.Background(), "UPDATE users SET name = $1", "ignored-input"); err != nil {
		t.Fatalf("exec: %v", err)
	}

	if under.converterCalls == 0 {
		t.Fatal("ColumnConverter was never consulted; the wrapper is hiding it")
	}
	if under.lastValue != ccSentinel {
		t.Errorf("argument reached the driver as %v, want the converter's sentinel %q", under.lastValue, ccSentinel)
	}
}

// TestStmtColumnConverterNotForcedWhenUnderlyingLacksOne verifies the converse:
// when the underlying statement does not implement ColumnConverter, the wrapper
// must not advertise one either, so database/sql applies its own default
// conversion and the value passes through unchanged.
func TestStmtColumnConverterNotForcedWhenUnderlyingLacksOne(t *testing.T) {
	under := &capturePlainStmt{}
	connector := connFactory(func() stddriver.Conn { return &captureConn{plain: under} })
	db := stdsql.OpenDB(qtdriver.WrapConnector(connector, testConfig()))
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.ExecContext(context.Background(), "UPDATE users SET name = $1", "kept"); err != nil {
		t.Fatalf("exec: %v", err)
	}

	if under.lastValue != "kept" {
		t.Errorf("argument reached the driver as %v, want %q (default conversion, no converter forced)", under.lastValue, "kept")
	}
}

// captureConn prepares a single, caller-supplied underlying statement so the
// test can inspect it after execution.
type captureConn struct {
	stmt  *ccStmt
	plain *capturePlainStmt
}

var (
	_ stddriver.Conn               = (*captureConn)(nil)
	_ stddriver.ConnPrepareContext = (*captureConn)(nil)
)

func (c *captureConn) PrepareContext(context.Context, string) (stddriver.Stmt, error) {
	return c.pick(), nil
}
func (c *captureConn) Prepare(string) (stddriver.Stmt, error) { return c.pick(), nil }
func (c *captureConn) pick() stddriver.Stmt {
	if c.stmt != nil {
		return c.stmt
	}
	return c.plain
}
func (c *captureConn) Close() error                 { return nil }
func (c *captureConn) Begin() (stddriver.Tx, error) { return fakeTx{}, nil }

// capturePlainStmt records the value it receives, with no ColumnConverter.
type capturePlainStmt struct{ lastValue stddriver.Value }

var _ stddriver.Stmt = (*capturePlainStmt)(nil)

func (s *capturePlainStmt) Close() error  { return nil }
func (s *capturePlainStmt) NumInput() int { return 1 }
func (s *capturePlainStmt) Exec(args []stddriver.Value) (stddriver.Result, error) {
	if len(args) == 1 {
		s.lastValue = args[0]
	}
	return fakeResult{}, nil
}
func (s *capturePlainStmt) Query([]stddriver.Value) (stddriver.Rows, error) {
	return &fakeRows{remaining: 0}, nil
}
