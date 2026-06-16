package driver

import (
	stddriver "database/sql/driver"

	qt "github.com/aita/sqlkit/querytrace"
)

// tx wraps a driver.Tx, recording its commit or rollback against the Trace that
// recorded its begin and clearing the open-transaction link on its connection so
// later statements are no longer attributed to it.
type tx struct {
	tr   *qt.Trace
	cn   *conn
	txID string
	tx   stddriver.Tx
}

var _ stddriver.Tx = (*tx)(nil)

// Commit commits the transaction and records the commit.
func (t *tx) Commit() error {
	err := t.tx.Commit()
	t.end(qt.TxCommit)
	return err
}

// Rollback rolls the transaction back and records the rollback.
func (t *tx) Rollback() error {
	err := t.tx.Rollback()
	t.end(qt.TxRollback)
	return err
}

// end records the transaction's commit or rollback and clears the connection's
// open-transaction id, so statements run on the reused connection afterward are
// no longer linked to this transaction.
func (t *tx) end(action qt.TxAction) {
	t.tr.RecordTxEnd(action, t.txID)
	if t.cn != nil && t.cn.txID == t.txID {
		t.cn.txID = ""
	}
}
