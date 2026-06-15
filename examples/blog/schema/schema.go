// Package schema declares the blog schema as a sqldef.Catalog. It is the
// source of truth: `go generate ./...` (from examples/blog) regenerates both
// the blogdb bindings and schema.sql from it via the sqlkit generate command.
package schema

import (
	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqldef"
)

// Catalog holds the blog catalog.
var Catalog = sqldef.NewCatalog()

// Public is the PostgreSQL schema the blog tables live in.
var Public = Catalog.Schema("public")

// Users are the registered blog authors.
var Users = Public.Table("users",
	sqldef.TableComment("Registered blog authors"),
	sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
	sqldef.Column("email", sqldef.VarChar(255), sqldef.Unique(), sqldef.Comment("Unique login address")),
	sqldef.Column("name", sqldef.Text, sqldef.Nullable()),
	sqldef.Column("created_at", sqldef.Timestamp, sqldef.Default(sql.Raw("now()"))),
)

// Posts reference the user that wrote them.
var Posts = Public.Table("posts",
	sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
	sqldef.Column("user_id", sqldef.BigInt, sqldef.References("users", "id")),
	sqldef.Column("title", sqldef.Text),
	sqldef.Column("body", sqldef.Text, sqldef.Nullable()),
	sqldef.Column("published", sqldef.Bool, sqldef.Default(sql.Raw("false"))),
	sqldef.Index("user_id"),
	sqldef.Check(sql.Raw("title <> ''")),
)

// Profiles hold one optional profile per user. The unique user_id makes it a
// has-one relation (Users.Rels.Profile), while Posts above is has-many.
var Profiles = Public.Table("profiles",
	sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
	sqldef.Column("user_id", sqldef.BigInt, sqldef.References("users", "id"), sqldef.Unique()),
	sqldef.Column("bio", sqldef.Text, sqldef.Nullable()),
	sqldef.Column("public", sqldef.Bool, sqldef.Default(sql.Raw("false"))),
)
