package sql

import (
	"fmt"
	"strconv"
	"strings"
)

// CreateSequenceStmt is the AST for a CREATE SEQUENCE statement (PostgreSQL). It
// is a DDL Statement. A nil numeric pointer means the option is absent (the
// engine default); Cycle is nil when neither CYCLE nor NO CYCLE was set. OwnedBy
// is "", "NONE", or "table.column".
type CreateSequenceStmt struct {
	posField
	Sequence    Table
	IfNotExists bool
	Increment   *int64
	MinValue    *int64
	NoMinValue  bool
	MaxValue    *int64
	NoMaxValue  bool
	Start       *int64
	Cache       *int64
	Cycle       *bool
	OwnedBy     Ident
}

// ToSQL compiles the CREATE SEQUENCE statement.
func (s CreateSequenceStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s CreateSequenceStmt) compileDDL(c *Compiler) (string, error) {
	if err := c.requirePostgresObject("sequences"); err != nil {
		return "", err
	}
	if s.Sequence.Name.IsZero() {
		return "", fmt.Errorf("sqlkit/sql: CREATE SEQUENCE requires a name")
	}
	var sb strings.Builder
	sb.WriteString("CREATE SEQUENCE ")
	if s.IfNotExists {
		sb.WriteString("IF NOT EXISTS ")
	}
	sb.WriteString(c.qualify(s.Sequence))
	if s.Increment != nil {
		sb.WriteString(" INCREMENT BY " + strconv.FormatInt(*s.Increment, 10))
	}
	switch {
	case s.NoMinValue:
		sb.WriteString(" NO MINVALUE")
	case s.MinValue != nil:
		sb.WriteString(" MINVALUE " + strconv.FormatInt(*s.MinValue, 10))
	}
	switch {
	case s.NoMaxValue:
		sb.WriteString(" NO MAXVALUE")
	case s.MaxValue != nil:
		sb.WriteString(" MAXVALUE " + strconv.FormatInt(*s.MaxValue, 10))
	}
	if s.Start != nil {
		sb.WriteString(" START WITH " + strconv.FormatInt(*s.Start, 10))
	}
	if s.Cache != nil {
		sb.WriteString(" CACHE " + strconv.FormatInt(*s.Cache, 10))
	}
	if s.Cycle != nil {
		if *s.Cycle {
			sb.WriteString(" CYCLE")
		} else {
			sb.WriteString(" NO CYCLE")
		}
	}
	if !s.OwnedBy.IsZero() {
		if s.OwnedBy.String() == "NONE" {
			sb.WriteString(" OWNED BY NONE")
		} else {
			sb.WriteString(" OWNED BY " + c.Dialect.QuoteIdent(s.OwnedBy.String()))
		}
	}
	sb.WriteString(";")
	return sb.String(), nil
}
func (CreateSequenceStmt) ddlStatement() {}

func (s CreateSequenceStmt) setPos(pos int) Query { s.NodePos = pos; return s }

// CreateSequenceBuilder is the fluent constructor for a CreateSequenceStmt.
type CreateSequenceBuilder struct{ s CreateSequenceStmt }

// CreateSequence starts a CREATE SEQUENCE builder for a sequence.
func CreateSequence(sequence Table) CreateSequenceBuilder {
	return CreateSequenceBuilder{s: CreateSequenceStmt{Sequence: sequence}}
}

// Stmt returns the built CREATE SEQUENCE statement.
func (b CreateSequenceBuilder) Stmt() CreateSequenceStmt { return b.s }

// IfNotExists adds IF NOT EXISTS.
func (b CreateSequenceBuilder) IfNotExists() CreateSequenceBuilder { b.s.IfNotExists = true; return b }

// Increment sets INCREMENT BY.
func (b CreateSequenceBuilder) Increment(n int64) CreateSequenceBuilder { b.s.Increment = &n; return b }

// Start sets START WITH.
func (b CreateSequenceBuilder) Start(n int64) CreateSequenceBuilder { b.s.Start = &n; return b }

// Cache sets CACHE.
func (b CreateSequenceBuilder) Cache(n int64) CreateSequenceBuilder { b.s.Cache = &n; return b }

// MinValue sets MINVALUE.
func (b CreateSequenceBuilder) MinValue(n int64) CreateSequenceBuilder { b.s.MinValue = &n; return b }

// NoMinValue sets NO MINVALUE.
func (b CreateSequenceBuilder) NoMinValue() CreateSequenceBuilder { b.s.NoMinValue = true; return b }

// MaxValue sets MAXVALUE.
func (b CreateSequenceBuilder) MaxValue(n int64) CreateSequenceBuilder { b.s.MaxValue = &n; return b }

// NoMaxValue sets NO MAXVALUE.
func (b CreateSequenceBuilder) NoMaxValue() CreateSequenceBuilder { b.s.NoMaxValue = true; return b }

// Cycle sets CYCLE.
func (b CreateSequenceBuilder) Cycle() CreateSequenceBuilder {
	cycle := true
	b.s.Cycle = &cycle
	return b
}

// NoCycle sets NO CYCLE.
func (b CreateSequenceBuilder) NoCycle() CreateSequenceBuilder {
	cycle := false
	b.s.Cycle = &cycle
	return b
}

// OwnedBy sets OWNED BY a "table.column", or "NONE" to detach the sequence.
func (b CreateSequenceBuilder) OwnedBy(column string) CreateSequenceBuilder {
	b.s.OwnedBy = NewIdent(column)
	return b
}

// ToSQL compiles the building CREATE SEQUENCE statement.
func (b CreateSequenceBuilder) ToSQL(d Dialect) (string, []any, error) { return b.s.ToSQL(d) }
func (b CreateSequenceBuilder) buildStmt() Query                       { return b.s }
func (CreateSequenceBuilder) ddlStatement()                            {}

func (b CreateSequenceBuilder) setPos(pos int) Query { b.s.NodePos = pos; return b }

// DropSequenceStmt is the AST for a DROP SEQUENCE statement.
type DropSequenceStmt struct {
	posField
	Names    []Ident
	IfExists bool
	Cascade  bool
}

// ToSQL compiles the DROP SEQUENCE statement.
func (s DropSequenceStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s DropSequenceStmt) compileDDL(c *Compiler) (string, error) {
	if err := c.requirePostgresObject("sequences"); err != nil {
		return "", err
	}
	if len(s.Names) == 0 {
		return "", fmt.Errorf("sqlkit/sql: DROP SEQUENCE requires a name")
	}
	var sb strings.Builder
	sb.WriteString("DROP SEQUENCE ")
	if s.IfExists {
		sb.WriteString("IF EXISTS ")
	}
	for i, name := range s.Names {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(c.Dialect.QuoteIdent(name.String()))
	}
	if s.Cascade {
		sb.WriteString(" CASCADE")
	}
	sb.WriteString(";")
	return sb.String(), nil
}
func (DropSequenceStmt) ddlStatement() {}

func (s DropSequenceStmt) setPos(pos int) Query { s.NodePos = pos; return s }

// DropSequenceBuilder is the fluent constructor for a DropSequenceStmt.
type DropSequenceBuilder struct{ s DropSequenceStmt }

// DropSequence starts a DROP SEQUENCE builder for one or more (possibly
// schema-qualified) sequence names.
func DropSequence(names ...string) DropSequenceBuilder {
	return DropSequenceBuilder{s: DropSequenceStmt{Names: idents(names)}}
}

// Stmt returns the built DROP SEQUENCE statement.
func (b DropSequenceBuilder) Stmt() DropSequenceStmt { return b.s }

// IfExists adds IF EXISTS.
func (b DropSequenceBuilder) IfExists() DropSequenceBuilder { b.s.IfExists = true; return b }

// Cascade adds CASCADE.
func (b DropSequenceBuilder) Cascade() DropSequenceBuilder { b.s.Cascade = true; return b }

// ToSQL compiles the building DROP SEQUENCE statement.
func (b DropSequenceBuilder) ToSQL(d Dialect) (string, []any, error) { return b.s.ToSQL(d) }
func (b DropSequenceBuilder) buildStmt() Query                       { return b.s }
func (DropSequenceBuilder) ddlStatement()                            {}

func (b DropSequenceBuilder) setPos(pos int) Query { b.s.NodePos = pos; return b }
