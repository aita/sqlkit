package uow

import (
	"database/sql"
	"testing"
)

// A nil pointer to a type whose Value method has a value receiver (the common
// nullable-column shape, e.g. *sql.NullString) must normalize to nil rather
// than reach the driver.Valuer branch, where calling Value on the nil pointer
// panics.
func TestNormalizeNilValuerPointer(t *testing.T) {
	var p *sql.NullString
	if got := normalize(p); got != nil {
		t.Fatalf("normalize(nil *sql.NullString) = %#v, want nil", got)
	}
	s := &sql.NullString{String: "x", Valid: true}
	if got := normalize(s); got != "x" {
		t.Fatalf("normalize(&NullString{x}) = %#v, want \"x\"", got)
	}
}
