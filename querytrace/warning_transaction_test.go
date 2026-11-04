package querytrace_test

import (
	"context"
	"testing"

	"github.com/aita/sqlkit/analysis"
	querytrace "github.com/aita/sqlkit/querytrace"
	"github.com/aita/sqlkit/sqltest"
)

// TestWriteOutsideTransactionWarns covers two writes issued with no transaction:
// they cannot roll back together, so querytrace flags them.
func TestWriteOutsideTransactionWarns(t *testing.T) {
	trace := querytrace.New("op", querytrace.WithConfig(querytrace.NewConfig()))
	for i := 0; i < 2; i++ {
		trace.RecordStatement(context.Background(), querytrace.Event{
			Kind:  querytrace.EventExec,
			Query: &querytrace.QueryInfo{Analysis: analysis.Analysis{Kind: querytrace.StmtInsert}},
		})
	}
	sqltest.Expect(t, trace).Warns(querytrace.WarnWriteOutsideTransaction)
}

// TestSingleWriteOutsideTransactionIsClean documents that one autocommit write is
// unremarkable — the warning is about multiple unbatched writes.
func TestSingleWriteOutsideTransactionIsClean(t *testing.T) {
	trace := querytrace.New("op", querytrace.WithConfig(querytrace.NewConfig()))
	trace.RecordStatement(context.Background(), querytrace.Event{
		Kind:  querytrace.EventExec,
		Query: &querytrace.QueryInfo{Analysis: analysis.Analysis{Kind: querytrace.StmtInsert}},
	})
	sqltest.Expect(t, trace).NoWarn(querytrace.WarnWriteOutsideTransaction)
}

// TestWritesInsideTransactionAreClean documents that writes bracketed by a
// transaction's begin and commit on the timeline do not trip the check.
func TestWritesInsideTransactionAreClean(t *testing.T) {
	trace := querytrace.New("op", querytrace.WithConfig(querytrace.NewConfig()))
	txID := trace.RecordTxBegin()
	for i := 0; i < 3; i++ {
		trace.RecordStatement(context.Background(), querytrace.Event{
			Kind:  querytrace.EventExec,
			Query: &querytrace.QueryInfo{Analysis: analysis.Analysis{Kind: querytrace.StmtInsert}},
		})
	}
	trace.RecordTxEnd(querytrace.TxCommit, txID)
	sqltest.Expect(t, trace).NoWarn(querytrace.WarnWriteOutsideTransaction)
}

// TestExternalCallInTransactionWarns covers a Mark placed between a transaction's
// begin and end: an application step that held the transaction open.
func TestExternalCallInTransactionWarns(t *testing.T) {
	trace := querytrace.New("op", querytrace.WithConfig(querytrace.NewConfig()))
	ctx := querytrace.WithTrace(context.Background(), trace)

	txID := trace.RecordTxBegin()
	querytrace.Mark(ctx, "charge payment API")
	trace.RecordTxEnd(querytrace.TxCommit, txID)

	sqltest.Expect(t, trace).Warns(querytrace.WarnExternalCallInTransaction)
}

// TestExternalCallInTransactionDeterministicTxID pins that when a Mark falls
// inside more than one still-open (never-committed) transaction, the warning is
// attributed to the same transaction on every derivation. The still-open spans
// were appended from a map, so under randomized iteration the reported tx_id
// flipped run to run; it must be the first-begun open transaction every time.
// Run enough identical traces that an accidental single-value result is
// negligible under the old map-order bug (which split ~85/15).
func TestExternalCallInTransactionDeterministicTxID(t *testing.T) {
	seen := map[string]int{}
	for i := 0; i < 200; i++ {
		trace := querytrace.New("op", querytrace.WithConfig(querytrace.NewConfig()))
		ctx := querytrace.WithTrace(context.Background(), trace)
		trace.RecordTxBegin() // tx_1, never committed (outer)
		trace.RecordTxBegin() // tx_2, never committed (inner)
		querytrace.Mark(ctx, "external")
		for _, w := range trace.Warnings() {
			if w.Code == querytrace.WarnExternalCallInTransaction {
				seen[w.Details["tx_id"].(string)]++
			}
		}
	}
	if len(seen) != 1 {
		t.Fatalf("tx_id attribution is nondeterministic across identical traces: %v", seen)
	}
	if _, ok := seen["tx_1"]; !ok {
		t.Errorf("expected the first-begun open transaction (tx_1) to be attributed, got %v", seen)
	}
}

// TestMarkOutsideTransactionIsClean documents that a Mark placed after the
// transaction commits is not flagged.
func TestMarkOutsideTransactionIsClean(t *testing.T) {
	trace := querytrace.New("op", querytrace.WithConfig(querytrace.NewConfig()))
	ctx := querytrace.WithTrace(context.Background(), trace)

	txID := trace.RecordTxBegin()
	trace.RecordTxEnd(querytrace.TxCommit, txID)
	querytrace.Mark(ctx, "charge payment API")

	sqltest.Expect(t, trace).NoWarn(querytrace.WarnExternalCallInTransaction)
}
