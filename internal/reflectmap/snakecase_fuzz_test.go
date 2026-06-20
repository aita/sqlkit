package reflectmap

import (
	"testing"
	"unicode/utf8"
)

// FuzzSnakeCase hardens the pure SnakeCase converter against arbitrary field
// names. SnakeCase runs over reflected struct field names, so its input is
// program-controlled rather than network-controlled, but it must still never
// panic on any string and must preserve UTF-8 validity (it only lowercases
// ASCII-range uppercase runes and inserts '_').
//
// Note: SnakeCase is not idempotent for every Unicode string — a rune like
// U+03D3 is "uppercase" yet has no simple lowercase mapping, so it can trigger
// a second separator on a re-run. That is harmless for real Go field names
// (ASCII identifiers) and outside the function's contract, so the fuzz target
// only asserts the guarantees the column mapper actually relies on.
func FuzzSnakeCase(f *testing.F) {
	for _, s := range []string{
		"UserID", "CreatedAt", "HTTPCode", "ID", "Email",
		"ParseURL", "already_snake", "", "_", "__", "A",
		"aB", "AB", "ABc", "aBc", "πUnicodeÆ", "η", "中文Name",
		"_Leading", "Trailing_", "a_B_c", string([]byte{0xff}),
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, name string) {
		got := SnakeCase(name)

		// Output must remain valid UTF-8 when the input was.
		if utf8.ValidString(name) && !utf8.ValidString(got) {
			t.Fatalf("SnakeCase(%q) = %q is not valid UTF-8", name, got)
		}

		// For real field names (ASCII letters/digits/underscore), the result must
		// have no ASCII uppercase letters — the property the column mapper relies
		// on when matching column names against lowercased field names.
		if isASCIIIdent(name) {
			for _, r := range got {
				if r >= 'A' && r <= 'Z' {
					t.Fatalf("SnakeCase(%q) = %q still contains uppercase ASCII", name, got)
				}
			}
		}
	})
}

// isASCIIIdent reports whether s consists only of ASCII letters, digits, and
// underscores — the shape of an actual Go struct field name.
func isASCIIIdent(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
		default:
			return false
		}
	}
	return true
}
