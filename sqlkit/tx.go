package sqlkit

import "database/sql"

// TxOptions describes transaction behavior requested by generated operations.
type TxOptions struct {
	Isolation sql.IsolationLevel
	ReadOnly  bool
}

// ToSQL converts sqlkit transaction options to database/sql options.
func (o TxOptions) ToSQL() *sql.TxOptions {
	return &sql.TxOptions{Isolation: o.Isolation, ReadOnly: o.ReadOnly}
}
