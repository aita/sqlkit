package sqlkit

import querysql "github.com/sqlkit-dev/sqlkit/sqlkit/sql"

// Compiler turns SQL expressions and queries into SQL text plus arguments.
type Compiler = querysql.Compiler

// NewCompiler creates a compiler for the supplied dialect.
func NewCompiler(dialect Dialect) *Compiler { return querysql.NewCompiler(dialect) }
