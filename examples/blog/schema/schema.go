// Package schema declares the blog schema as a decl.Catalog. It is the
// source of truth: `go generate ./...` (from examples/blog) regenerates both
// the blogdb bindings and schema.sql from it via the sqlkit generate command.
package schema

import (
	"github.com/aita/sqlkit/decl"
	"github.com/aita/sqlkit/sql"
)

// Catalog holds the blog catalog.
var Catalog = decl.NewCatalog()

// Public is the PostgreSQL schema the blog tables live in.
var Public = Catalog.Schema("public")

// Users are the registered blog authors.
var Users = Public.Table("users",
	decl.TableComment("Registered blog authors"),
	decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
	decl.Column("email", decl.VarChar(255), decl.Unique(), decl.Comment("Unique login address")),
	decl.Column("name", decl.Text, decl.Nullable()),
	decl.Column("created_at", decl.Timestamp, decl.Default(sql.Raw("now()"))),
)

// Posts reference the user that wrote them.
var Posts = Public.Table("posts",
	decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
	decl.Column("user_id", decl.BigInt, decl.References("users", "id")),
	decl.Column("title", decl.Text),
	decl.Column("body", decl.Text, decl.Nullable()),
	decl.Column("published", decl.Bool, decl.Default(sql.Raw("false"))),
	decl.Index("user_id"),
	decl.Check(sql.Raw("title <> ''")),
)

// Profiles hold one optional profile per user. The unique user_id makes it a
// has-one relation (Users.Rels.Profile), while Posts above is has-many.
var Profiles = Public.Table("profiles",
	decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
	decl.Column("user_id", decl.BigInt, decl.References("users", "id"), decl.Unique()),
	decl.Column("bio", decl.Text, decl.Nullable()),
	decl.Column("public", decl.Bool, decl.Default(sql.Raw("false"))),
)
