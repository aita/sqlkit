package querytrace_test

import (
	"context"
	"testing"

	querytrace "github.com/aita/sqlkit/x/querytrace"
	"github.com/aita/sqlkit/x/querytrace/testkit"
)

// nplusOneConfig enables repeated-query detection at the given threshold,
// optionally ignoring some operations. MaxQueries stays 0 so only N+1 warnings
// are observed.
func nplusOneConfig(min int, ignore ...querytrace.StmtKind) querytrace.Config {
	return querytrace.Config{
		Enabled:     true,
		Fingerprint: true,
		AnalyzeSQL:  true,
		NPlusOne:    querytrace.NPlusOneConfig{Enabled: true, MinCount: min, IgnoreOperations: ignore},
	}
}

func at(file string, line int) *querytrace.Caller {
	return &querytrace.Caller{File: file, Line: line}
}

func recordQuery(t *querytrace.Trace, fp string, op querytrace.StmtKind, c *querytrace.Caller) {
	t.RecordStatement(context.Background(), querytrace.Event{
		Kind:   querytrace.EventQuery,
		Query:  &querytrace.QueryInfo{Fingerprint: fp, Operation: op},
		Caller: c,
	})
}

func TestRepeatedFromOneSiteIsLoop(t *testing.T) {
	trace := querytrace.New("op", querytrace.WithConfig(nplusOneConfig(3)))
	loop := at("svc.go", 10)
	for i := 0; i < 3; i++ {
		recordQuery(trace, "SELECT * FROM comments WHERE post_id = ?", querytrace.StmtSelect, loop)
	}
	testkit.AssertWarning(t, trace, querytrace.WarnQueryInLoop)
}

func TestRepeatedAcrossSitesIsPossibleNPlusOne(t *testing.T) {
	trace := querytrace.New("op", querytrace.WithConfig(nplusOneConfig(3)))
	sites := []*querytrace.Caller{at("a.go", 1), at("b.go", 2), at("c.go", 3)}
	for _, c := range sites {
		recordQuery(trace, "SELECT * FROM users WHERE id = ?", querytrace.StmtSelect, c)
	}
	testkit.AssertWarning(t, trace, querytrace.WarnPossibleNPlusOne)
}

func TestRepeatedWithoutCallerIsPossibleNPlusOne(t *testing.T) {
	trace := querytrace.New("op", querytrace.WithConfig(nplusOneConfig(3)))
	for i := 0; i < 3; i++ {
		recordQuery(trace, "SELECT * FROM users WHERE id = ?", querytrace.StmtSelect, nil)
	}
	testkit.AssertWarning(t, trace, querytrace.WarnPossibleNPlusOne)
}

func TestRepeatedBelowThresholdIsClean(t *testing.T) {
	trace := querytrace.New("op", querytrace.WithConfig(nplusOneConfig(3)))
	loop := at("svc.go", 10)
	// One short of the threshold: no warning.
	for i := 0; i < 2; i++ {
		recordQuery(trace, "SELECT * FROM comments WHERE post_id = ?", querytrace.StmtSelect, loop)
	}
	testkit.AssertNoNPlusOne(t, trace)
}

func TestRepeatedIgnoredOperationIsClean(t *testing.T) {
	trace := querytrace.New("op", querytrace.WithConfig(nplusOneConfig(3, querytrace.StmtInsert)))
	site := at("log.go", 7)
	for i := 0; i < 3; i++ {
		recordQuery(trace, "INSERT INTO audit_log (msg) VALUES (?)", querytrace.StmtInsert, site)
	}
	testkit.AssertNoNPlusOne(t, trace)

	// A non-ignored operation at the same threshold still warns.
	for i := 0; i < 3; i++ {
		recordQuery(trace, "SELECT * FROM audit_log WHERE id = ?", querytrace.StmtSelect, site)
	}
	testkit.AssertWarning(t, trace, querytrace.WarnQueryInLoop)
}

func TestRepeatedDetectionDisabled(t *testing.T) {
	cfg := nplusOneConfig(3)
	cfg.NPlusOne.Enabled = false
	trace := querytrace.New("op", querytrace.WithConfig(cfg))
	loop := at("svc.go", 10)
	for i := 0; i < 5; i++ {
		recordQuery(trace, "SELECT * FROM comments WHERE post_id = ?", querytrace.StmtSelect, loop)
	}
	testkit.AssertNoNPlusOne(t, trace)
}
