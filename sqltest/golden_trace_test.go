package sqltest_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aita/sqlkit/analysis"
	"github.com/aita/sqlkit/querytrace"
	"github.com/aita/sqlkit/sqltest"
)

// traceWithDuration records the same two statements and a transaction, taking d
// per statement, so two runs differ only in timing.
func traceWithDuration(d time.Duration) *querytrace.Trace {
	tr := querytrace.New("load")
	ctx := context.Background()
	start := time.Now()
	tx := tr.RecordTxBegin()
	for i, fp := range []string{"select * from users where id = ?", "select * from posts where user_id = ?"} {
		at := start.Add(time.Duration(i+1) * time.Second)
		tr.RecordStatement(ctx, querytrace.Event{
			Kind:      querytrace.EventQuery,
			StartedAt: at,
			EndedAt:   at.Add(d),
			Duration:  d,
			Query:     &querytrace.QueryInfo{Analysis: analysis.Analysis{Fingerprint: fp}},
		})
	}
	tr.RecordTxEnd(querytrace.TxCommit, tx)
	return tr
}

// The golden form of a trace must not depend on timing: the same operation run
// fast or slow renders identical text, so AssertGoldenTrace is stable against a
// real database.
func TestAssertGoldenTraceIgnoresTiming(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.golden")
	t.Setenv("SQLKIT_UPDATE_GOLDEN", "1")
	sqltest.AssertGoldenTrace(t, path, traceWithDuration(time.Millisecond))
	t.Setenv("SQLKIT_UPDATE_GOLDEN", "")
	sqltest.AssertGoldenTrace(t, path, traceWithDuration(3*time.Second))

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	for _, want := range []string{"SQL trace: load", "queries=2", "select * from users where id = ?", "TX"} {
		if !strings.Contains(text, want) {
			t.Errorf("golden trace missing %q:\n%s", want, text)
		}
	}
	for _, bad := range []string{"total_db_time", "ms", "warnings"} {
		if strings.Contains(text, bad) {
			t.Errorf("golden trace contains timing-dependent %q:\n%s", bad, text)
		}
	}
}
