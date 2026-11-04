package reflectmap

import (
	"strings"
	"unicode"
)

// SnakeCase converts a Go field name to snake_case: UserID -> user_id,
// CreatedAt -> created_at, HTTPCode -> http_code.
func SnakeCase(name string) string {
	runes := []rune(name)
	var sb strings.Builder
	for i, r := range runes {
		if unicode.IsUpper(r) {
			prevLower := i > 0 && !unicode.IsUpper(runes[i-1])
			// A digit continues the current acronym/word rather than starting a new
			// one (UserID2 -> user_id2, not user_i_d2), so only an actual lowercase
			// letter after this uppercase run counts as "the next word started".
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if i > 0 && (prevLower || nextLower) && runes[i-1] != '_' {
				sb.WriteByte('_')
			}
			sb.WriteRune(unicode.ToLower(r))
			continue
		}
		sb.WriteRune(r)
	}
	return sb.String()
}
