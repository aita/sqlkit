package driver

import (
	stddriver "database/sql/driver"

	qt "github.com/aita/sqlkit/querytrace"
)

// tx wraps a driver.Tx, recording its commit or rollback against the Trace that
// recorded its begin.
type tx struct {
	tr   *qt.Trace
	txID string
	tx   stddriver.Tx
}

var _ stddriver.Tx = (*tx)(nil)

// Commit commits the transaction and records the commit.
func (t *tx) Commit() error {
	err := t.tx.Commit()
	t.tr.RecordTxEnd(qt.TxCommit, t.txID)
	return err
}

// Rollback rolls the transaction back and records the rollback.
func (t *tx) Rollback() error {
	err := t.tx.Rollback()
	t.tr.RecordTxEnd(qt.TxRollback, t.txID)
	return err
}
