package postgres

import (
	"strings"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse"
	pg "github.com/pganalyze/pg_query_go/v6"
)

// convertGrant converts a GRANT or REVOKE on database objects. Forms it does not
// model (ALL ... IN SCHEMA targets, column-level privileges, object types other
// than those in grantObjectType) report Unsupported, so Parse falls back to a
// verbatim RawQuery.
func convertGrant(s *pg.GrantStmt) (sql.Query, error) {
	if s.GetTargtype() != pg.GrantTargetType_ACL_TARGET_OBJECT {
		return nil, sqlparse.Unsupportedf("GRANT target type %s", s.GetTargtype())
	}
	objectType, err := grantObjectType(s.GetObjtype())
	if err != nil {
		return nil, err
	}
	objects, err := grantObjects(s.GetObjects())
	if err != nil {
		return nil, err
	}
	privileges, err := grantPrivileges(s.GetPrivileges())
	if err != nil {
		return nil, err
	}
	grantees, err := grantRoles(s.GetGrantees())
	if err != nil {
		return nil, err
	}

	if s.GetIsGrant() {
		g := sql.Grant(privileges...)
		// TABLE is the implicit default; emit the idiomatic On so a plain
		// "GRANT ... ON x" round-trips without pinning the object type keyword.
		if objectType == "TABLE" {
			g = g.On(objects...)
		} else {
			g = g.OnType(objectType, objects...)
		}
		g = g.To(grantees...)
		if s.GetGrantOption() {
			g = g.WithGrantOption()
		}
		return g, nil
	}

	r := sql.Revoke(privileges...)
	if objectType == "TABLE" {
		r = r.On(objects...)
	} else {
		r = r.OnType(objectType, objects...)
	}
	r = r.From(grantees...)
	if s.GetGrantOption() {
		r = r.GrantOptionFor()
	}
	switch s.GetBehavior() {
	case pg.DropBehavior_DROP_CASCADE:
		r = r.Cascade()
	case pg.DropBehavior_DROP_RESTRICT:
		r = r.Restrict()
	}
	return r, nil
}

// grantObjectType maps a privilege object type to its keyword. Only the object
// types whose objects are named relations/identifiers are modeled.
func grantObjectType(t pg.ObjectType) (string, error) {
	switch t {
	case pg.ObjectType_OBJECT_TABLE:
		return "TABLE", nil
	case pg.ObjectType_OBJECT_SEQUENCE:
		return "SEQUENCE", nil
	case pg.ObjectType_OBJECT_SCHEMA:
		return "SCHEMA", nil
	case pg.ObjectType_OBJECT_DATABASE:
		return "DATABASE", nil
	default:
		return "", sqlparse.Unsupportedf("GRANT object type %s", t)
	}
}

// grantObjects renders each object node as a qualified name. Tables/sequences
// arrive as RangeVar; schemas/databases as String identifiers.
func grantObjects(nodes []*pg.Node) ([]string, error) {
	objects := make([]string, 0, len(nodes))
	for _, node := range nodes {
		switch {
		case node.GetRangeVar() != nil:
			rv := node.GetRangeVar()
			name := rv.GetRelname()
			if rv.GetSchemaname() != "" {
				name = rv.GetSchemaname() + "." + name
			}
			objects = append(objects, name)
		case node.GetString_() != nil:
			objects = append(objects, node.GetString_().GetSval())
		default:
			return nil, sqlparse.Unsupportedf("GRANT object %T", node.GetNode())
		}
	}
	return objects, nil
}

// grantPrivileges renders the privilege keywords, uppercased; an empty list is
// ALL PRIVILEGES. Column-level privileges are not modeled.
func grantPrivileges(nodes []*pg.Node) ([]string, error) {
	if len(nodes) == 0 {
		// An empty privilege list is ALL PRIVILEGES; keep it nil so it matches the
		// zero slice an idiomatic Grant()/Revoke() leaves.
		return nil, nil
	}
	privileges := make([]string, 0, len(nodes))
	for _, node := range nodes {
		priv := node.GetAccessPriv()
		if priv == nil {
			return nil, sqlparse.Unsupportedf("privilege %T", node.GetNode())
		}
		if len(priv.GetCols()) > 0 {
			return nil, sqlparse.Unsupported("column-level privilege")
		}
		privileges = append(privileges, strings.ToUpper(priv.GetPrivName()))
	}
	return privileges, nil
}

// grantRoles renders each grantee, mapping the PUBLIC pseudo-role to "PUBLIC".
func grantRoles(nodes []*pg.Node) ([]string, error) {
	grantees := make([]string, 0, len(nodes))
	for _, node := range nodes {
		role := node.GetRoleSpec()
		if role == nil {
			return nil, sqlparse.Unsupportedf("grantee %T", node.GetNode())
		}
		switch role.GetRoletype() {
		case pg.RoleSpecType_ROLESPEC_PUBLIC:
			grantees = append(grantees, "PUBLIC")
		case pg.RoleSpecType_ROLESPEC_CSTRING:
			grantees = append(grantees, role.GetRolename())
		default:
			return nil, sqlparse.Unsupportedf("grantee role type %s", role.GetRoletype())
		}
	}
	return grantees, nil
}
