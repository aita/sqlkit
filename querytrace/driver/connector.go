package driver

import (
	"context"
	stddriver "database/sql/driver"

	qt "github.com/aita/sqlkit/querytrace"
)

// connector wraps a driver.Connector so the connections it hands out are
// instrumented.
type connector struct {
	cfg qt.Config
	c   stddriver.Connector
}

// WrapConnector wraps c so statements run over the connections it produces are
// recorded against the querytrace.Trace in their context, using cfg. Open a
// database/sql handle over the result with sql.OpenDB.
func WrapConnector(c stddriver.Connector, cfg qt.Config) stddriver.Connector {
	return &connector{cfg: cfg, c: c}
}

// Connect opens an instrumented connection.
func (w *connector) Connect(ctx context.Context) (stddriver.Conn, error) {
	c, err := w.c.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return newConn(w.cfg, c), nil
}

// Driver returns the underlying driver.
func (w *connector) Driver() stddriver.Driver { return w.c.Driver() }
