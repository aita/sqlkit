package rowset

import "iter"

// Iterator is the range-over-func iterator shape used by RowSet.
type Iterator = iter.Seq[Row]

// Iter returns an iterator that can be used directly in a for range loop.
func (rs RowSet) Iter() Iterator {
	return func(yield func(Row) bool) {
		rows, err := rs.collectRows()
		if err != nil {
			return
		}
		for _, row := range rows {
			if !yield(row) {
				return
			}
		}
	}
}

// Rows returns an iterator that can be used directly in a for range loop.
func (rs RowSet) Rows() Iterator { return rs.Iter() }
