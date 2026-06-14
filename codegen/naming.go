package codegen

import "strings"

// commonInitialisms are rendered fully uppercase in Go identifiers.
var commonInitialisms = map[string]bool{
	"acl": true, "api": true, "ascii": true, "cpu": true, "css": true,
	"db": true, "dns": true, "eof": true, "guid": true, "html": true,
	"http": true, "https": true, "id": true, "ip": true, "json": true,
	"lhs": true, "qps": true, "ram": true, "rhs": true, "rpc": true,
	"sla": true, "smtp": true, "sql": true, "ssh": true, "tcp": true,
	"tls": true, "ttl": true, "udp": true, "ui": true, "uid": true,
	"uuid": true, "uri": true, "url": true, "utf8": true, "vm": true,
	"xml": true, "xmpp": true, "xsrf": true, "xss": true,
}

// goName converts a snake_case database identifier to an exported Go name.
func goName(name string) string {
	var sb strings.Builder
	for _, part := range strings.FieldsFunc(name, func(r rune) bool { return r == '_' || r == ' ' || r == '-' }) {
		lower := strings.ToLower(part)
		if commonInitialisms[lower] {
			sb.WriteString(strings.ToUpper(lower))
			continue
		}
		sb.WriteString(strings.ToUpper(lower[:1]))
		sb.WriteString(lower[1:])
	}
	if sb.Len() == 0 {
		return "X"
	}
	return sb.String()
}

// singular returns the singular form of a snake_case identifier's last word,
// using a small set of English rules. It is best-effort: callers dedupe the
// resulting Go names against reserved identifiers.
func singular(name string) string {
	if name == "" {
		return name
	}
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, "ies") && len(name) > 3:
		return name[:len(name)-3] + "y"
	case strings.HasSuffix(lower, "ches"), strings.HasSuffix(lower, "shes"),
		strings.HasSuffix(lower, "sses"), strings.HasSuffix(lower, "xes"),
		strings.HasSuffix(lower, "zes"):
		return name[:len(name)-2]
	case strings.HasSuffix(lower, "ss"):
		return name // e.g. "address" stays "address"
	case strings.HasSuffix(lower, "s"):
		return name[:len(name)-1]
	}
	return name
}

// reservedFieldNames are method names of the generated table binding; column
// fields that would collide get a Col suffix.
var reservedFieldNames = map[string]bool{
	"Info": true, "Columns": true, "PrimaryKey": true, "Defaulted": true,
	"SQLSource": true, "Get": true,
	// Relation-aware bindings add these methods/fields to the table struct.
	"Query": true, "Q": true, "Rels": true,
}

func fieldName(column string) string {
	name := goName(column)
	if reservedFieldNames[name] {
		return name + "Col"
	}
	return name
}

// goKeywords are not usable as parameter names in generated signatures.
var goKeywords = map[string]bool{
	"break": true, "case": true, "chan": true, "const": true, "continue": true,
	"default": true, "defer": true, "else": true, "fallthrough": true,
	"for": true, "func": true, "go": true, "goto": true, "if": true,
	"import": true, "interface": true, "map": true, "package": true,
	"range": true, "return": true, "select": true, "struct": true,
	"switch": true, "type": true, "var": true,
}

// paramName converts a Go field name to a parameter name: ID -> id,
// UserID -> userID. Go keywords and shadowed names get a trailing underscore.
func paramName(field string) string {
	runes := []rune(field)
	upper := 0
	for upper < len(runes) && runes[upper] >= 'A' && runes[upper] <= 'Z' {
		upper++
	}
	// Lowercase the leading initialism (ID -> id, URLPath -> urlPath), but keep
	// the last upper rune when it starts the next word (UserID stays userID via
	// the single leading rune path).
	if upper > 1 && upper < len(runes) {
		upper--
	}
	for i := 0; i < max(upper, 1) && i < len(runes); i++ {
		if runes[i] >= 'A' && runes[i] <= 'Z' {
			runes[i] += 'a' - 'A'
		}
	}
	name := string(runes)
	if goKeywords[name] || name == "ctx" || name == "s" || name == "t" || name == "ex" || name == "dest" {
		name += "_"
	}
	return name
}
