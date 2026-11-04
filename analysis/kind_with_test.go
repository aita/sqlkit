package analysis_test

import (
	"testing"

	"github.com/aita/sqlkit/analysis"
)

// DetectKind's keyword fallback (no parser, a parse failure, or a raw
// passthrough the parser could not model) used to classify every WITH
// statement as a read, so WITH ... INSERT/UPDATE/DELETE and a data-modifying
// CTE slipped past ForbidWrites/ReadOnly. The statement the CTE list prefixes
// decides the kind, and a writing or locking CTE body makes it write-like.
func TestDetectKindWith(t *testing.T) {
	cases := []struct {
		text            string
		kind            analysis.StmtKind
		viaCTE, locking bool
	}{
		{"WITH d AS (SELECT 1) SELECT * FROM d", analysis.StmtSelect, false, false},
		{"with d as (select 1) insert into t select * from d", analysis.StmtInsert, false, false},
		{"WITH RECURSIVE d(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM d) UPDATE t SET x = 1", analysis.StmtUpdate, false, false},
		{"WITH d AS (SELECT id FROM t) DELETE FROM t WHERE id IN (SELECT id FROM d)", analysis.StmtDelete, false, false},
		{"WITH d AS (DELETE FROM t RETURNING id) SELECT * FROM d", analysis.StmtSelect, true, false},
		{"WITH d AS MATERIALIZED (UPDATE t SET x = 1 RETURNING id) SELECT * FROM d", analysis.StmtSelect, true, false},
		{"WITH d AS (SELECT * FROM t FOR UPDATE) SELECT * FROM d", analysis.StmtSelect, false, true},
		{"WITH d AS (SELECT * FROM t FOR NO KEY UPDATE) SELECT * FROM d", analysis.StmtSelect, false, true},
		{"WITH d AS (SELECT 1) SELECT * FROM d FOR SHARE", analysis.StmtSelect, false, true},
		// Keywords inside literals, quoted names and comments don't count.
		{`WITH d AS (SELECT 'delete from t', "update" FROM t /* insert */) SELECT * FROM d -- update`, analysis.StmtSelect, false, false},
		{`WITH d AS (SELECT 'it''s (' AS x) SELECT * FROM d`, analysis.StmtSelect, false, false},
		// A trailing backslash ends a PostgreSQL string but escapes the quote in
		// MySQL; either reading must still see the write.
		{`WITH d AS (SELECT 'C:\' AS p) INSERT INTO t SELECT * FROM d`, analysis.StmtInsert, false, false},
		{`WITH d AS (SELECT 'it\'s' AS p) DELETE FROM t`, analysis.StmtDelete, false, false},
	}
	for _, tc := range cases {
		if got := analysis.DetectKind(tc.text); got != tc.kind {
			t.Errorf("DetectKind(%q) = %v, want %v", tc.text, got, tc.kind)
		}
		an := analysis.New().Analyze(tc.text, nil)
		if an.Kind != tc.kind || an.WritesViaCTE != tc.viaCTE || an.LockingRead != tc.locking {
			t.Errorf("Analyze(%q) = kind %v viaCTE %v locking %v, want %v %v %v",
				tc.text, an.Kind, an.WritesViaCTE, an.LockingRead, tc.kind, tc.viaCTE, tc.locking)
		}
		if want := tc.kind != analysis.StmtSelect || tc.viaCTE || tc.locking; an.IsWrite() != want {
			t.Errorf("Analyze(%q).IsWrite() = %v, want %v", tc.text, an.IsWrite(), want)
		}
	}
}
