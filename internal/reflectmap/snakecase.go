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
			nextLower := i+1 < len(runes) && !unicode.IsUpper(runes[i+1]) && runes[i+1] != '_'
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
