package mysql

import "database/sql/driver"

// Decimal is the exact textual form of a parsed DECIMAL/NUMERIC literal. MySQL's
// fixed-point literals (for example 0.10, 100.00 or 123456789.123456789) carry a
// precise value and scale that a float64 cannot hold without loss, so the parser
// preserves the original digits here rather than rounding through a float.
//
// It is carried as a bind argument. database/sql calls Value on driver.Valuer
// arguments, so the exact decimal text reaches the driver unchanged; Decimal also
// implements fmt.Stringer for display.
type Decimal string

// Value implements driver.Valuer, yielding the exact decimal text.
func (d Decimal) Value() (driver.Value, error) { return string(d), nil }

// String returns the exact decimal text.
func (d Decimal) String() string { return string(d) }
