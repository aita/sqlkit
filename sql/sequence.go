package sql

import (
	"fmt"
	"strconv"
	"strings"
)

// CreateSequenceStmt builds a CREATE SEQUENCE statement (PostgreSQL). It is a
// DDL Statement. Unset options are omitted, taking the engine default.
type CreateSequenceStmt struct {
	sequence    Table
	ifNotExists bool
	increment   *int64
	minValue    *int64
	noMinValue  bool
	maxValue    *int64
	noMaxValue  bool
	start       *int64
	cache       *int64
	hasCycle    bool
	cycle       bool
	ownedBy     string // "" unset, "NONE", or "table.column"
}

// CreateSequence starts a CREATE SEQUENCE builder for a sequence.
func CreateSequence(sequence Table) CreateSequenceStmt {
	return CreateSequenceStmt{sequence: sequence}
}

// IfNotExists adds IF NOT EXISTS.
func (s CreateSequenceStmt) IfNotExists() CreateSequenceStmt { s.ifNotExists = true; return s }

// Increment sets INCREMENT BY.
func (s CreateSequenceStmt) Increment(n int64) CreateSequenceStmt { s.increment = &n; return s }

// Start sets START WITH.
func (s CreateSequenceStmt) Start(n int64) CreateSequenceStmt { s.start = &n; return s }

// Cache sets CACHE.
func (s CreateSequenceStmt) Cache(n int64) CreateSequenceStmt { s.cache = &n; return s }

// MinValue sets MINVALUE.
func (s CreateSequenceStmt) MinValue(n int64) CreateSequenceStmt { s.minValue = &n; return s }

// NoMinValue sets NO MINVALUE.
func (s CreateSequenceStmt) NoMinValue() CreateSequenceStmt { s.noMinValue = true; return s }

// MaxValue sets MAXVALUE.
func (s CreateSequenceStmt) MaxValue(n int64) CreateSequenceStmt { s.maxValue = &n; return s }

// NoMaxValue sets NO MAXVALUE.
func (s CreateSequenceStmt) NoMaxValue() CreateSequenceStmt { s.noMaxValue = true; return s }

// Cycle sets CYCLE.
func (s CreateSequenceStmt) Cycle() CreateSequenceStmt { s.hasCycle, s.cycle = true, true; return s }

// NoCycle sets NO CYCLE.
func (s CreateSequenceStmt) NoCycle() CreateSequenceStmt { s.hasCycle, s.cycle = true, false; return s }

// OwnedBy sets OWNED BY a "table.column", or "NONE" to detach the sequence.
func (s CreateSequenceStmt) OwnedBy(column string) CreateSequenceStmt { s.ownedBy = column; return s }

// ToSQL compiles the CREATE SEQUENCE statement.
func (s CreateSequenceStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s CreateSequenceStmt) compileDDL(c *Compiler) (string, error) {
	if s.sequence.Name == "" {
		return "", fmt.Errorf("sqlkit/sql: CREATE SEQUENCE requires a name")
	}
	var sb strings.Builder
	sb.WriteString("CREATE SEQUENCE ")
	if s.ifNotExists {
		sb.WriteString("IF NOT EXISTS ")
	}
	sb.WriteString(c.qualify(s.sequence))
	if s.increment != nil {
		sb.WriteString(" INCREMENT BY " + strconv.FormatInt(*s.increment, 10))
	}
	switch {
	case s.noMinValue:
		sb.WriteString(" NO MINVALUE")
	case s.minValue != nil:
		sb.WriteString(" MINVALUE " + strconv.FormatInt(*s.minValue, 10))
	}
	switch {
	case s.noMaxValue:
		sb.WriteString(" NO MAXVALUE")
	case s.maxValue != nil:
		sb.WriteString(" MAXVALUE " + strconv.FormatInt(*s.maxValue, 10))
	}
	if s.start != nil {
		sb.WriteString(" START WITH " + strconv.FormatInt(*s.start, 10))
	}
	if s.cache != nil {
		sb.WriteString(" CACHE " + strconv.FormatInt(*s.cache, 10))
	}
	if s.hasCycle {
		if s.cycle {
			sb.WriteString(" CYCLE")
		} else {
			sb.WriteString(" NO CYCLE")
		}
	}
	if s.ownedBy != "" {
		if s.ownedBy == "NONE" {
			sb.WriteString(" OWNED BY NONE")
		} else {
			sb.WriteString(" OWNED BY " + c.Dialect.QuoteIdent(s.ownedBy))
		}
	}
	sb.WriteString(";")
	return sb.String(), nil
}
func (CreateSequenceStmt) ddlStatement() {}

// DropSequenceStmt builds a DROP SEQUENCE statement.
type DropSequenceStmt struct {
	names    []string
	ifExists bool
	cascade  bool
}

// DropSequence starts a DROP SEQUENCE builder for one or more (possibly
// schema-qualified) sequence names.
func DropSequence(names ...string) DropSequenceStmt {
	return DropSequenceStmt{names: names}
}

// IfExists adds IF EXISTS.
func (s DropSequenceStmt) IfExists() DropSequenceStmt { s.ifExists = true; return s }

// Cascade adds CASCADE.
func (s DropSequenceStmt) Cascade() DropSequenceStmt { s.cascade = true; return s }

// ToSQL compiles the DROP SEQUENCE statement.
func (s DropSequenceStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s DropSequenceStmt) compileDDL(c *Compiler) (string, error) {
	if len(s.names) == 0 {
		return "", fmt.Errorf("sqlkit/sql: DROP SEQUENCE requires a name")
	}
	var sb strings.Builder
	sb.WriteString("DROP SEQUENCE ")
	if s.ifExists {
		sb.WriteString("IF EXISTS ")
	}
	for i, name := range s.names {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(c.Dialect.QuoteIdent(name))
	}
	if s.cascade {
		sb.WriteString(" CASCADE")
	}
	sb.WriteString(";")
	return sb.String(), nil
}
func (DropSequenceStmt) ddlStatement() {}
