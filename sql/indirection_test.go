package sql

import "testing"

func TestUpdateSubscriptAssignmentCompile(t *testing.T) {
	q := Update(Table{Name: NewIdent("t")}).Set(SetAt(Subscript(Col("tags"), 1), "x")).Where(Eq(Col("id"), 5))
	got, args, err := q.ToSQL(PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	want := `UPDATE "t" SET "tags"[$1] = $2 WHERE ("id" = $3)`
	if got != want {
		t.Fatalf("ToSQL() =\n  %q\nwant\n  %q", got, want)
	}
	if len(args) != 3 || args[0] != 1 || args[1] != "x" || args[2] != 5 {
		t.Fatalf("args = %#v, want [1 x 5]", args)
	}
}

func TestIndirectionCompile(t *testing.T) {
	cases := []struct {
		expr     Expression
		want     string
		wantArgs int
	}{
		{Subscript(Col("tags"), 1), `"tags"[$1]`, 1},
		{Slice(Col("tags"), Val(1), Val(3)), `"tags"[$1:$2]`, 2},
		{Slice(Col("tags"), nil, Val(3)), `"tags"[:$1]`, 1},
		{Field(Col("rec"), "name"), `("rec")."name"`, 0},
		{Collate(Col("name"), "C"), `"name" COLLATE "C"`, 0},
		// A non-column operand (an array constructor) must be parenthesized before
		// the subscript, or "ARRAY[1,2,3][1]" is invalid SQL.
		{Subscript(Array(1, 2, 3), 1), `(ARRAY[$1, $2, $3])[$4]`, 4},
		// A field access already parenthesizes itself, so it subscripts bare.
		{Subscript(Field(Col("rec"), "tags"), 1), `("rec")."tags"[$1]`, 1},
	}
	for _, tc := range cases {
		got, args, err := NewCompiler(PostgreSQLDialect{}).CompileExpression(tc.expr)
		if err != nil {
			t.Fatalf("CompileExpression() error = %v", err)
		}
		if got != tc.want {
			t.Errorf("CompileExpression() = %q, want %q", got, tc.want)
		}
		if len(args) != tc.wantArgs {
			t.Errorf("args = %#v, want %d", args, tc.wantArgs)
		}
	}
}
