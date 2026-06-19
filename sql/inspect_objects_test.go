package sql_test

import (
	"testing"

	"github.com/aita/sqlkit/sql"
)

func TestMergeStmt(t *testing.T) {
	p := sql.MergeInto(sql.Table{Name: "accounts"}).As("t").
		Using(sql.SourceAlias(sql.Table{Name: "staging"}, "s")).
		On(sql.Eq(sql.Col("t.id"), sql.Col("s.id"))).
		When(sql.WhenMatched().ThenUpdate(sql.Set("balance", sql.Col("s.balance")))).
		When(sql.WhenNotMatched().ThenInsert([]string{"id"}, sql.Col("s.id"))).
		Stmt()

	if p.Target.Name != "accounts" || p.TargetAlias != "t" {
		t.Errorf("Target/alias = %q/%q, want accounts/t", p.Target.Name, p.TargetAlias)
	}
	if p.Using == nil || p.On == nil {
		t.Fatal("Using/On missing from MergeStmt")
	}
	if len(p.When) != 2 {
		t.Fatalf("When = %d clauses, want 2", len(p.When))
	}
	if p.When[0].Match != "MATCHED" || p.When[0].Action != "UPDATE" || len(p.When[0].Sets) != 1 {
		t.Errorf("When[0] = %+v, want MATCHED UPDATE with one assignment", p.When[0])
	}
	if p.When[1].Match != "NOT MATCHED" || p.When[1].Action != "INSERT" ||
		len(p.When[1].InsertColumns) != 1 || len(p.When[1].InsertValues) != 1 {
		t.Errorf("When[1] = %+v, want NOT MATCHED INSERT with one column/value", p.When[1])
	}
}

// Walk descends into a MERGE's USING subquery.
func TestMergeWalk(t *testing.T) {
	using := sql.Select(sql.Col("id"), sql.Col("balance")).From(sql.Table{Name: "staging"})
	q := sql.MergeInto(sql.Table{Name: "accounts"}).
		Using(sql.Alias(using, "s")).
		On(sql.Eq(sql.Col("accounts.id"), sql.Col("s.id"))).
		When(sql.WhenMatched().ThenDelete())

	var visited int
	sql.Walk(q, func(sql.Query) bool { visited++; return true })
	if visited != 2 {
		t.Errorf("Walk over MERGE visited %d queries, want 2 (merge + USING subquery)", visited)
	}
}

func TestNewStatementParts(t *testing.T) {
	g := sql.Grant("SELECT", "INSERT").On("employees").To("analyst").WithGrantOption().Parts()
	if len(g.Privileges) != 2 || len(g.Objects) != 1 || len(g.Grantees) != 1 || !g.WithGrantOption {
		t.Errorf("GrantParts = %+v", g)
	}
	r := sql.RenameColumn(sql.Table{Name: "users"}, "email", "addr").Parts()
	if r.Table.Name != "users" || r.OldName != "email" || r.NewName != "addr" {
		t.Errorf("RenameColumnParts = %+v", r)
	}
	seq := sql.CreateSequence(sql.Table{Name: "s"}).Start(5).NoMaxValue().Cycle().Parts()
	if seq.Start == nil || *seq.Start != 5 {
		t.Errorf("CreateSequenceParts.Start = %v, want 5", seq.Start)
	}
	if !seq.NoMaxValue || seq.Cycle == nil || !*seq.Cycle {
		t.Errorf("CreateSequenceParts = %+v, want NoMaxValue and Cycle=true", seq)
	}
	if e := sql.CreateEnum("mood", "sad", "ok").Parts(); e.Name != "mood" || len(e.Values) != 2 {
		t.Errorf("CreateEnumParts = %+v", e)
	}
}
