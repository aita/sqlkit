package sqltest_test

import (
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/sqltest"
)

// TestHelperSignaturesAcceptTestingTB locks in that the seeding and isolation
// helpers take a testing.TB rather than a concrete *testing.T, so they remain
// usable from benchmarks and shared testing.TB setup. The assignments fail to
// compile if a signature regresses to *testing.T.
func TestHelperSignaturesAcceptTestingTB(t *testing.T) {
	var _ func(testing.TB, sqlkit.Querier, sqlkit.Table, ...any) = sqltest.InsertRows
	var _ func(testing.TB, sqlkit.Querier, sqlkit.Table, any) int64 = sqltest.InsertID
	var _ func(testing.TB, *sqlkit.Session) func(testing.TB) = sqltest.Snapshot
	var _ func(testing.TB, sqlkit.Querier) = sqltest.NewFixture("x").Load
}
