package querytrace

import "github.com/aita/sqlkit/analyze"

// StmtKind classifies a SQL statement by the kind of statement it represents. It
// is an alias for analyze.StmtKind: the statement analysis lives in the analyze
// package, but querytrace keeps the name so QueryInfo.Kind and
// NPlusOne.IgnoreOperations stay typed and the StmtXxx constants below read as
// part of the querytrace API.
type StmtKind = analyze.StmtKind

// The statement kinds, re-exported from analyze so querytrace callers (and
// querytest matchers) refer to them without importing analyze directly.
const (
	StmtUnknown = analyze.StmtUnknown
	StmtSelect  = analyze.StmtSelect
	StmtInsert  = analyze.StmtInsert
	StmtUpdate  = analyze.StmtUpdate
	StmtDelete  = analyze.StmtDelete
	StmtDDL     = analyze.StmtDDL
)
