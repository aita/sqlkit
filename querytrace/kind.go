package querytrace

import "github.com/aita/sqlkit/analysis"

// StmtKind classifies a SQL statement by the kind of statement it represents. It
// is an alias for analysis.StmtKind: the statement analysis lives in the analyze
// package, but querytrace keeps the name so QueryInfo.Kind and
// NPlusOne.IgnoreOperations stay typed and the StmtXxx constants below read as
// part of the querytrace API.
type StmtKind = analysis.StmtKind

// The statement kinds, re-exported from analyze so querytrace callers (and
// querytest matchers) refer to them without importing analyze directly.
const (
	StmtUnknown = analysis.StmtUnknown
	StmtSelect  = analysis.StmtSelect
	StmtInsert  = analysis.StmtInsert
	StmtUpdate  = analysis.StmtUpdate
	StmtDelete  = analysis.StmtDelete
	StmtDDL     = analysis.StmtDDL
	StmtMerge   = analysis.StmtMerge
)
