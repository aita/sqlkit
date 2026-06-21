package querytrace_test

import (
	"context"
	"testing"

	"github.com/aita/sqlkit/analyze"
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
			Query: &querytrace.QueryInfo{Analysis: analyze.Analysis{Kind: querytrace.StmtInsert}},
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
		Query: &querytrace.QueryInfo{Analysis: analyze.Analysis{Kind: querytrace.StmtInsert}},
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
			Query: &querytrace.QueryInfo{Analysis: analyze.Analysis{Kind: querytrace.StmtInsert}},
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
