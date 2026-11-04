package sqltest_test

import (
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/sqltest"
)

// TestHelperSignaturesAcceptTestingTB locks in that the seeding and isolation
// helpers take a testing.TB rather than a concrete *testing.T, so they remain
// usable from benchmarks and shared testing.TB setup. Each helper is passed to a
// parameter of its exact expected type, so the test fails to compile if a
// signature regresses to *testing.T.
func TestHelperSignaturesAcceptTestingTB(t *testing.T) {
	wantInsertRows(sqltest.InsertRows)
	wantInsertID(sqltest.InsertID)
	wantSnapshot(sqltest.Snapshot)
	wantFixtureLoad(sqltest.NewFixture("x").Load)
}

func wantInsertRows(func(testing.TB, sqlkit.Querier, sqlkit.Table, ...any))  {}
func wantInsertID(func(testing.TB, sqlkit.Querier, sqlkit.Table, any) int64) {}
func wantSnapshot(func(testing.TB, *sqlkit.Session) func(testing.TB))        {}
func wantFixtureLoad(func(testing.TB, sqlkit.Querier))                       {}
