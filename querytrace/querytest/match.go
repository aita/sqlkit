package querytest

import (
	"fmt"
	"regexp"
	"strings"

	querytrace "github.com/aita/sqlkit/querytrace"
)

// Match is a predicate over a recorded Event. Matchers select the statements an
// assertion or a spy query applies to; pass several to Query, Count, or Events
// and they must all hold (logical AND). All, Any, and Not compose them
// explicitly.
//
// Most matchers look at the statement: Select/Insert/Update/Delete, Table,
// Fingerprint, HasWhere, Params, SqlContains. A few look at the surrounding
// Event (Named, Caller, Label, InTransaction) or the transaction timeline
// (Begin, Commit, Rollback). Matchers that depend on structural analysis
// (Table, HasWhere, Fingerprint) need a parser wired into the Config; without
// one they do not match, while operation and SQL-text matchers still do.
type Match struct {
	desc string
	fn   func(querytrace.Event) bool
}

// String renders the matcher for failure messages.
func (m Match) String() string { return m.desc }

// query builds a Match that applies pred to an Event's QueryInfo, holding only
// for statement events that carry one.
func query(desc string, pred func(*querytrace.QueryInfo) bool) Match {
	return Match{desc: desc, fn: func(ev querytrace.Event) bool {
		return ev.Query != nil && pred(ev.Query)
	}}
}

// Op matches a statement of the given kind.
func Op(k querytrace.StmtKind) Match {
	return query("op="+k.String(), func(qi *querytrace.QueryInfo) bool { return qi.Operation == k })
}

// Select matches a SELECT (or other read).
func Select() Match { return Op(querytrace.StmtSelect) }

// Insert matches an INSERT.
func Insert() Match { return Op(querytrace.StmtInsert) }

// Update matches an UPDATE.
func Update() Match { return Op(querytrace.StmtUpdate) }

// Delete matches a DELETE.
func Delete() Match { return Op(querytrace.StmtDelete) }

// Ddl matches a schema statement.
func Ddl() Match { return Op(querytrace.StmtDDL) }

// Read matches any read statement.
func Read() Match {
	return query("read", func(qi *querytrace.QueryInfo) bool { return qi.Operation.IsRead() })
}

// Write matches any write statement (INSERT/UPDATE/DELETE).
func Write() Match {
	return query("write", func(qi *querytrace.QueryInfo) bool { return qi.Operation.IsWrite() })
}

// Table matches a statement that references table, by name and case-insensitively.
// It needs structural analysis (a parser in the Config).
func Table(name string) Match {
	return query("table="+name, func(qi *querytrace.QueryInfo) bool {
		for _, tb := range qi.Tables {
			if strings.EqualFold(tb, name) {
				return true
			}
		}
		return false
	})
}

// Fingerprint matches a statement with the given normalized fingerprint.
func Fingerprint(fp string) Match {
	return query("fingerprint="+fp, func(qi *querytrace.QueryInfo) bool { return qi.Fingerprint == fp })
}

// HasWhere matches a statement that carries a WHERE clause (from analysis).
func HasWhere() Match {
	return query("has_where", func(qi *querytrace.QueryInfo) bool { return qi.HasWhere })
}

// HasJoin matches a statement that carries a JOIN clause (from analysis).
func HasJoin() Match {
	return query("has_join", func(qi *querytrace.QueryInfo) bool { return qi.HasJoin })
}

// Params matches a statement carrying exactly n bind arguments.
func Params(n int) Match {
	return query(fmt.Sprintf("params=%d", n), func(qi *querytrace.QueryInfo) bool { return qi.ParamCount == n })
}

// ParamsAtLeast matches a statement carrying at least n bind arguments, the
// signature of a large IN list or batch.
func ParamsAtLeast(n int) Match {
	return query(fmt.Sprintf("params>=%d", n), func(qi *querytrace.QueryInfo) bool { return qi.ParamCount >= n })
}

// Sql matches a statement whose raw SQL equals text, ignoring surrounding
// whitespace. It needs Config.CaptureRawSQL.
func Sql(text string) Match {
	want := strings.TrimSpace(text)
	return query("sql="+want, func(qi *querytrace.QueryInfo) bool {
		return strings.TrimSpace(qi.RawSQL) == want
	})
}

// SqlContains matches a statement whose raw SQL contains sub, case-insensitively.
// It needs Config.CaptureRawSQL.
func SqlContains(sub string) Match {
	low := strings.ToLower(sub)
	return query("sql~"+sub, func(qi *querytrace.QueryInfo) bool {
		return strings.Contains(strings.ToLower(qi.RawSQL), low)
	})
}

// SqlMatches matches a statement whose raw SQL matches the regular expression
// re. It panics if re does not compile. It needs Config.CaptureRawSQL.
func SqlMatches(re string) Match {
	rx := regexp.MustCompile(re)
	return query("sql=~"+re, func(qi *querytrace.QueryInfo) bool { return rx.MatchString(qi.RawSQL) })
}

// Named matches a statement tagged with the given query name (WithQueryName or
// Named).
func Named(name string) Match {
	return Match{desc: "name=" + name, fn: func(ev querytrace.Event) bool { return ev.Name == name }}
}

// Caller matches a statement issued from application code whose function name or
// file path contains sub. It needs Config.CaptureCaller.
func Caller(sub string) Match {
	return Match{desc: "caller~" + sub, fn: func(ev querytrace.Event) bool {
		return ev.Caller != nil && (strings.Contains(ev.Caller.Function, sub) || strings.Contains(ev.Caller.File, sub))
	}}
}

// Label matches a statement carrying the label key=value (WithLabel).
func Label(key, value string) Match {
	return Match{desc: fmt.Sprintf("label[%s]=%s", key, value), fn: func(ev querytrace.Event) bool {
		return ev.Labels[key] == value
	}}
}

// Begin matches a transaction begin on the timeline.
func Begin() Match { return txMatch("begin", querytrace.TxBegin) }

// Commit matches a transaction commit on the timeline.
func Commit() Match { return txMatch("commit", querytrace.TxCommit) }

// Rollback matches a transaction rollback on the timeline.
func Rollback() Match { return txMatch("rollback", querytrace.TxRollback) }

func txMatch(desc string, action querytrace.TxAction) Match {
	return Match{desc: "tx=" + desc, fn: func(ev querytrace.Event) bool {
		return ev.Kind == querytrace.EventTx && ev.Tx != nil && ev.Tx.Action == action
	}}
}

// All matches an Event only when every Match in ms does.
func All(ms ...Match) Match {
	return Match{desc: "(" + joinDesc(ms, " and ") + ")", fn: func(ev querytrace.Event) bool {
		return matchAll(ev, ms)
	}}
}

// Any matches an Event when any Match in ms does.
func Any(ms ...Match) Match {
	return Match{desc: "(" + joinDesc(ms, " or ") + ")", fn: func(ev querytrace.Event) bool {
		for _, m := range ms {
			if m.fn(ev) {
				return true
			}
		}
		return false
	}}
}

// Not inverts a Match.
func Not(m Match) Match {
	return Match{desc: "not " + m.desc, fn: func(ev querytrace.Event) bool { return !m.fn(ev) }}
}

// matchAll reports whether ev satisfies every matcher in ms.
func matchAll(ev querytrace.Event, ms []Match) bool {
	for _, m := range ms {
		if !m.fn(ev) {
			return false
		}
	}
	return true
}

// describe renders a matcher set for failure messages.
func describe(ms []Match) string {
	if len(ms) == 0 {
		return "any statement"
	}
	return joinDesc(ms, " ")
}

func joinDesc(ms []Match, sep string) string {
	parts := make([]string, len(ms))
	for i, m := range ms {
		parts[i] = m.desc
	}
	return strings.Join(parts, sep)
}
