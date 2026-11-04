package sqlkit

import "testing"

// A fold node's single-column key may be a []byte (a BINARY/bytea UUID primary
// key). Used directly as a map key it panics ("hash of unhashable type"); it
// must yield a usable key that still tells distinct values apart.
func TestIdentityKeyNonComparableSingleKey(t *testing.T) {
	seen := map[any]int{}
	for _, v := range [][]byte{{1, 2}, {1, 3}, {1, 2}} {
		seen[identityKey([]any{v})]++
	}
	if len(seen) != 2 || seen[identityKey([]any{[]byte{1, 2}})] != 2 {
		t.Fatalf("[]byte keys grouped as %v, want {1,2}x2 and {1,3}x1", seen)
	}
	if identityKey([]any{int64(7)}) != int64(7) {
		t.Fatal("a comparable single key must stay itself")
	}
}
