package sql

import "testing"

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
