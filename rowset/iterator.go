package rowset

import "iter"

// Iterator is the range-over-func iterator shape used by RowSet. It yields
// each row with a nil error; if reading fails, the final yield carries the
// error with a zero Row.
type Iterator = iter.Seq2[Row, error]

// Iter returns a streaming iterator usable directly in a for range loop:
//
//	for row, err := range rs.Iter() {
//		if err != nil { return err }
//		...
//	}
//
// Rows are fetched on demand; breaking out of the loop stops reading and
// releases the underlying result set.
func (rs RowSet) Iter() Iterator {
	return func(yield func(Row, error) bool) {
		err := rs.iterate(func(row Row) bool { return yield(row, nil) })
		if err != nil {
			yield(Row{}, err)
		}
	}
}

// Rows returns a streaming iterator; see Iter.
func (rs RowSet) Rows() Iterator { return rs.Iter() }
