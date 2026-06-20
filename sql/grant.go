package sql

import (
	"fmt"
	"strings"
)

// GrantStmt is the AST for a GRANT statement granting privileges on database
// objects to roles. An empty privilege list means ALL PRIVILEGES; an empty
// object type defaults to TABLE. It is a DDL Statement, so it compiles with no
// bind values.
type GrantStmt struct {
	posField
	Privileges      []string
	ObjectType      string
	Objects         []Ident
	Grantees        []Ident
	WithGrantOption bool
}

// ToSQL compiles the GRANT statement.
func (s GrantStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s GrantStmt) compileDDL(c *Compiler) (string, error) {
	return c.CompileGrant(s)
}
func (GrantStmt) ddlStatement() {}

func (s GrantStmt) setPos(pos int) Query { s.NodePos = pos; return s }

// GrantBuilder is the fluent constructor for a GrantStmt.
type GrantBuilder struct{ s GrantStmt }

// Grant starts a GRANT builder for the given privilege keywords (e.g. "SELECT",
// "INSERT"). Pass none for ALL PRIVILEGES.
func Grant(privileges ...string) GrantBuilder {
	return GrantBuilder{s: GrantStmt{Privileges: privileges}}
}

// Stmt returns the built GRANT statement.
func (b GrantBuilder) Stmt() GrantStmt { return b.s }

// On sets the objects the privileges apply to, with the default object type
// TABLE (GRANT ... ON TABLE objects ...).
func (b GrantBuilder) On(objects ...string) GrantBuilder {
	b.s.Objects = idents(objects)
	return b
}

// OnType sets the object type keyword (e.g. "SCHEMA", "SEQUENCE", "DATABASE")
// and the objects the privileges apply to.
func (b GrantBuilder) OnType(objectType string, objects ...string) GrantBuilder {
	b.s.ObjectType = objectType
	b.s.Objects = idents(objects)
	return b
}

// To sets the grantee roles. Use "PUBLIC" for the PUBLIC pseudo-role.
func (b GrantBuilder) To(grantees ...string) GrantBuilder {
	b.s.Grantees = idents(grantees)
	return b
}

// WithGrantOption adds WITH GRANT OPTION, letting grantees grant the privileges
// onward.
func (b GrantBuilder) WithGrantOption() GrantBuilder {
	b.s.WithGrantOption = true
	return b
}

// ToSQL compiles the building GRANT statement.
func (b GrantBuilder) ToSQL(d Dialect) (string, []any, error) { return b.s.ToSQL(d) }
func (b GrantBuilder) buildStmt() Query                       { return b.s }
func (GrantBuilder) ddlStatement()                            {}

func (b GrantBuilder) setPos(pos int) Query { b.s.NodePos = pos; return b }

// RevokeStmt is the AST for a REVOKE statement removing privileges on database
// objects from roles. It mirrors GrantStmt and is likewise a DDL Statement.
// Behavior is "", "CASCADE", or "RESTRICT".
type RevokeStmt struct {
	posField
	GrantOptionFor bool
	Privileges     []string
	ObjectType     string
	Objects        []Ident
	Grantees       []Ident
	Behavior       string
}

// ToSQL compiles the REVOKE statement.
func (s RevokeStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s RevokeStmt) compileDDL(c *Compiler) (string, error) {
	return c.CompileRevoke(s)
}
func (RevokeStmt) ddlStatement() {}

func (s RevokeStmt) setPos(pos int) Query { s.NodePos = pos; return s }

// RevokeBuilder is the fluent constructor for a RevokeStmt.
type RevokeBuilder struct{ s RevokeStmt }

// Revoke starts a REVOKE builder for the given privilege keywords. Pass none for
// ALL PRIVILEGES.
func Revoke(privileges ...string) RevokeBuilder {
	return RevokeBuilder{s: RevokeStmt{Privileges: privileges}}
}

// Stmt returns the built REVOKE statement.
func (b RevokeBuilder) Stmt() RevokeStmt { return b.s }

// GrantOptionFor revokes only the grant option (REVOKE GRANT OPTION FOR ...),
// leaving the privileges themselves.
func (b RevokeBuilder) GrantOptionFor() RevokeBuilder {
	b.s.GrantOptionFor = true
	return b
}

// On sets the objects to revoke from, with the default object type TABLE.
func (b RevokeBuilder) On(objects ...string) RevokeBuilder {
	b.s.Objects = idents(objects)
	return b
}

// OnType sets the object type keyword and the objects to revoke from.
func (b RevokeBuilder) OnType(objectType string, objects ...string) RevokeBuilder {
	b.s.ObjectType = objectType
	b.s.Objects = idents(objects)
	return b
}

// From sets the roles to revoke from. Use "PUBLIC" for the PUBLIC pseudo-role.
func (b RevokeBuilder) From(grantees ...string) RevokeBuilder {
	b.s.Grantees = idents(grantees)
	return b
}

// Cascade adds CASCADE, also revoking dependent privileges.
func (b RevokeBuilder) Cascade() RevokeBuilder {
	b.s.Behavior = "CASCADE"
	return b
}

// Restrict adds RESTRICT, refusing to revoke when dependent privileges exist.
func (b RevokeBuilder) Restrict() RevokeBuilder {
	b.s.Behavior = "RESTRICT"
	return b
}

// ToSQL compiles the building REVOKE statement.
func (b RevokeBuilder) ToSQL(d Dialect) (string, []any, error) { return b.s.ToSQL(d) }
func (b RevokeBuilder) buildStmt() Query                       { return b.s }
func (RevokeBuilder) ddlStatement()                            {}

func (b RevokeBuilder) setPos(pos int) Query { b.s.NodePos = pos; return b }

// CompileGrant compiles a GRANT statement.
func (c *Compiler) CompileGrant(s GrantStmt) (string, error) {
	body, err := c.grantBody(s.Privileges, s.ObjectType, s.Objects, s.Grantees)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	sb.WriteString("GRANT ")
	sb.WriteString(body)
	if s.WithGrantOption {
		sb.WriteString(" WITH GRANT OPTION")
	}
	sb.WriteString(";")
	return sb.String(), nil
}

// CompileRevoke compiles a REVOKE statement.
func (c *Compiler) CompileRevoke(s RevokeStmt) (string, error) {
	var sb strings.Builder
	sb.WriteString("REVOKE ")
	if s.GrantOptionFor {
		sb.WriteString("GRANT OPTION FOR ")
	}
	body, err := c.grantBody(s.Privileges, s.ObjectType, s.Objects, s.Grantees)
	if err != nil {
		return "", err
	}
	// grantBody renders the grantee clause with TO; REVOKE uses FROM.
	sb.WriteString(strings.Replace(body, " TO ", " FROM ", 1))
	if s.Behavior != "" {
		sb.WriteString(" ")
		sb.WriteString(s.Behavior)
	}
	sb.WriteString(";")
	return sb.String(), nil
}

// grantBody renders "<privileges> ON <type> <objects> TO <grantees>", shared by
// GRANT and REVOKE (REVOKE rewrites TO to FROM).
func (c *Compiler) grantBody(privileges []string, objectType string, objects, grantees []Ident) (string, error) {
	if len(objects) == 0 {
		return "", fmt.Errorf("sqlkit/sql: GRANT/REVOKE requires at least one object")
	}
	if len(grantees) == 0 {
		return "", fmt.Errorf("sqlkit/sql: GRANT/REVOKE requires at least one grantee")
	}
	var sb strings.Builder
	if len(privileges) == 0 {
		sb.WriteString("ALL PRIVILEGES")
	} else {
		sb.WriteString(strings.Join(privileges, ", "))
	}
	if objectType == "" {
		objectType = "TABLE"
	}
	sb.WriteString(" ON ")
	sb.WriteString(objectType)
	sb.WriteString(" ")
	for i, object := range objects {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(c.Dialect.QuoteIdent(object.String()))
	}
	sb.WriteString(" TO ")
	for i, grantee := range grantees {
		if i > 0 {
			sb.WriteString(", ")
		}
		if grantee.String() == "PUBLIC" {
			sb.WriteString("PUBLIC")
			continue
		}
		sb.WriteString(c.Dialect.QuoteName(grantee.String()))
	}
	return sb.String(), nil
}
