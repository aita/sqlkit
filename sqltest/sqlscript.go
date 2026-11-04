package sqltest

import "strings"

// scriptStmt is one statement of a hand-written SQL script: text is the
// statement as written (comments included), code is the same statement with
// every comment blanked out, for pattern matching that must not see comments.
type scriptStmt struct {
	text string
	code string
}

// splitScript breaks a hand-written SQL script into statements on top-level
// semicolons. Unlike splitStatements (which only has to handle sqlkit's own
// rendered DDL) it skips semicolons inside single-quoted strings, quoted or
// back-quoted identifiers, -- and /* */ comments, and PostgreSQL dollar-quoted
// bodies ($$...$$, $tag$...$tag$). With mysql set it also honours MySQL's
// backslash escapes in strings and # comments, and does not treat $ as a quote.
// Empty statements (only whitespace and comments) are dropped.
func splitScript(script string, mysql bool) []scriptStmt {
	var out []scriptStmt
	var text, code strings.Builder
	flush := func() {
		if strings.TrimSpace(code.String()) != "" {
			out = append(out, scriptStmt{text: strings.TrimSpace(text.String()), code: strings.TrimSpace(code.String())})
		}
		text.Reset()
		code.Reset()
	}
	// emit appends s to the statement text, and to the comment-free code unless
	// it is a comment (blanked to a space so tokens on either side stay apart).
	emit := func(s string, comment bool) {
		text.WriteString(s)
		if comment {
			code.WriteByte(' ')
		} else {
			code.WriteString(s)
		}
	}
	n := len(script)
	for i := 0; i < n; {
		c := script[i]
		switch {
		case c == ';':
			flush()
			i++
		case c == '\'' || c == '"' || c == '`':
			j := i + 1
			for j < n {
				if mysql && c == '\'' && script[j] == '\\' && j+1 < n {
					j += 2
					continue
				}
				if script[j] == c {
					if j+1 < n && script[j+1] == c { // doubled quote: an escaped quote
						j += 2
						continue
					}
					j++
					break
				}
				j++
			}
			if j > n {
				j = n
			}
			emit(script[i:j], false)
			i = j
		case c == '-' && i+1 < n && script[i+1] == '-', mysql && c == '#':
			j := strings.IndexByte(script[i:], '\n')
			if j < 0 {
				j = n - i
			}
			emit(script[i:i+j], true)
			i += j
		case c == '/' && i+1 < n && script[i+1] == '*':
			j := strings.Index(script[i+2:], "*/")
			end := n
			if j >= 0 {
				end = i + 2 + j + 2
			}
			emit(script[i:end], true)
			i = end
		case !mysql && c == '$' && (i == 0 || !isIdentByte(script[i-1])):
			if tag, ok := dollarTag(script[i:]); ok {
				end := n
				if j := strings.Index(script[i+len(tag):], tag); j >= 0 {
					end = i + len(tag) + j + len(tag)
				}
				emit(script[i:end], false)
				i = end
				continue
			}
			emit(script[i:i+1], false)
			i++
		default:
			emit(script[i:i+1], false)
			i++
		}
	}
	flush()
	return out
}

// dollarTag returns the opening dollar-quote tag ($$ or $name$) at the start of
// s, if any. A tag name follows identifier rules and cannot start with a digit,
// so a positional parameter such as $1 is not mistaken for one.
func dollarTag(s string) (string, bool) {
	for j := 1; j < len(s); j++ {
		switch b := s[j]; {
		case b == '$':
			return s[:j+1], true
		case b == '_' || (b|0x20 >= 'a' && b|0x20 <= 'z') || b >= 0x80:
		case b >= '0' && b <= '9' && j > 1:
		default:
			return "", false
		}
	}
	return "", false
}

func isIdentByte(b byte) bool {
	return b == '_' || (b|0x20 >= 'a' && b|0x20 <= 'z') || (b >= '0' && b <= '9') || b >= 0x80
}
