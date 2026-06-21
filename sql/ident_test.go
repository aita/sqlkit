package sql

import "testing"

func TestIdentHelpers(t *testing.T) {
	id := NewIdentAt("Foo", 5)
	if id.Name != "Foo" || id.Pos() != 5 {
		t.Fatalf("NewIdentAt = %+v, pos %d", id, id.Pos())
	}
	if got := id.Fold().Name; got != "foo" {
		t.Fatalf("Fold() = %q, want foo", got)
	}
	if !NewIdent(" Foo ").Equal(NewIdent("foo")) {
		t.Fatal("Equal: folded names should match")
	}
	if NewIdent("a").Equal(NewIdent("b")) {
		t.Fatal("Equal: distinct names should not match")
	}
	if got := IdentNames(nil); got != nil {
		t.Fatalf("IdentNames(nil) = %v, want nil", got)
	}
	got := IdentNames([]Ident{NewIdent("a"), NewIdent("b")})
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("IdentNames = %v, want [a b]", got)
	}
}
