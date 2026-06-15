package sql

import (
	"fmt"
	"strings"
)

// GrantStmt builds a GRANT statement granting privileges on database objects to
// roles. An empty privilege list means ALL PRIVILEGES; an empty object type
// defaults to TABLE. It is a DDL Statement, so it compiles with no bind values.
type GrantStmt struct {
	privileges      []string
	objectType      string
	objects         []string
	grantees        []string
	withGrantOption bool
}

// Grant starts a GRANT builder for the given privilege keywords (e.g. "SELECT",
// "INSERT"). Pass none for ALL PRIVILEGES.
func Grant(privileges ...string) GrantStmt {
	return GrantStmt{privileges: privileges}
}

// On sets the objects the privileges apply to, with the default object type
// TABLE (GRANT ... ON TABLE objects ...).
func (s GrantStmt) On(objects ...string) GrantStmt {
	s.objects = objects
	return s
}

// OnType sets the object type keyword (e.g. "SCHEMA", "SEQUENCE", "DATABASE")
// and the objects the privileges apply to.
func (s GrantStmt) OnType(objectType string, objects ...string) GrantStmt {
	s.objectType = objectType
	s.objects = objects
	return s
}

// To sets the grantee roles. Use "PUBLIC" for the PUBLIC pseudo-role.
func (s GrantStmt) To(grantees ...string) GrantStmt {
	s.grantees = grantees
	return s
}

// WithGrantOption adds WITH GRANT OPTION, letting grantees grant the privileges
// onward.
func (s GrantStmt) WithGrantOption() GrantStmt {
	s.withGrantOption = true
	return s
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

// RevokeStmt builds a REVOKE statement removing privileges on database objects
// from roles. It mirrors GrantStmt and is likewise a DDL Statement.
type RevokeStmt struct {
	grantOptionFor bool
	privileges     []string
	objectType     string
	objects        []string
	grantees       []string
	behavior       string // "", "CASCADE", or "RESTRICT"
}

// Revoke starts a REVOKE builder for the given privilege keywords. Pass none for
// ALL PRIVILEGES.
func Revoke(privileges ...string) RevokeStmt {
	return RevokeStmt{privileges: privileges}
}

// GrantOptionFor revokes only the grant option (REVOKE GRANT OPTION FOR ...),
// leaving the privileges themselves.
func (s RevokeStmt) GrantOptionFor() RevokeStmt {
	s.grantOptionFor = true
	return s
}

// On sets the objects to revoke from, with the default object type TABLE.
func (s RevokeStmt) On(objects ...string) RevokeStmt {
	s.objects = objects
	return s
}

// OnType sets the object type keyword and the objects to revoke from.
func (s RevokeStmt) OnType(objectType string, objects ...string) RevokeStmt {
	s.objectType = objectType
	s.objects = objects
	return s
}

// From sets the roles to revoke from. Use "PUBLIC" for the PUBLIC pseudo-role.
func (s RevokeStmt) From(grantees ...string) RevokeStmt {
	s.grantees = grantees
	return s
}

// Cascade adds CASCADE, also revoking dependent privileges.
func (s RevokeStmt) Cascade() RevokeStmt {
	s.behavior = "CASCADE"
	return s
}

// Restrict adds RESTRICT, refusing to revoke when dependent privileges exist.
func (s RevokeStmt) Restrict() RevokeStmt {
	s.behavior = "RESTRICT"
	return s
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

// CompileGrant compiles a GRANT statement.
func (c *Compiler) CompileGrant(s GrantStmt) (string, error) {
	body, err := c.grantBody(s.privileges, s.objectType, s.objects, s.grantees)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	sb.WriteString("GRANT ")
	sb.WriteString(body)
	if s.withGrantOption {
		sb.WriteString(" WITH GRANT OPTION")
	}
	sb.WriteString(";")
	return sb.String(), nil
}

// CompileRevoke compiles a REVOKE statement.
func (c *Compiler) CompileRevoke(s RevokeStmt) (string, error) {
	var sb strings.Builder
	sb.WriteString("REVOKE ")
	if s.grantOptionFor {
		sb.WriteString("GRANT OPTION FOR ")
	}
	body, err := c.grantBody(s.privileges, s.objectType, s.objects, s.grantees)
	if err != nil {
		return "", err
	}
	// grantBody renders the grantee clause with TO; REVOKE uses FROM.
	sb.WriteString(strings.Replace(body, " TO ", " FROM ", 1))
	if s.behavior != "" {
		sb.WriteString(" ")
		sb.WriteString(s.behavior)
	}
	sb.WriteString(";")
	return sb.String(), nil
}

// grantBody renders "<privileges> ON <type> <objects> TO <grantees>", shared by
// GRANT and REVOKE (REVOKE rewrites TO to FROM).
func (c *Compiler) grantBody(privileges []string, objectType string, objects, grantees []string) (string, error) {
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
		sb.WriteString(c.Dialect.QuoteIdent(object))
	}
	sb.WriteString(" TO ")
	for i, grantee := range grantees {
		if i > 0 {
			sb.WriteString(", ")
		}
		if grantee == "PUBLIC" {
			sb.WriteString("PUBLIC")
			continue
		}
		sb.WriteString(c.Dialect.QuoteName(grantee))
	}
	return sb.String(), nil
}
