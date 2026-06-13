// Package design declares the blog schema as a schema.Catalog. It is the
// source of truth: `go run ./gen` (from examples/blog) regenerates both the
// blogdb bindings and schema.sql from it.
package design

import "github.com/aita/sqlkit/schema"

// Catalog holds the blog design.
var Catalog = schema.NewCatalog()

// Public is the PostgreSQL schema the blog tables live in.
var Public = Catalog.Schema("public")

// Users are the registered blog authors.
var Users = Public.Table("users",
	schema.TableComment("Registered blog authors"),
	schema.Column("id", schema.BigInt, schema.PrimaryKey(), schema.Default()),
	schema.Column("email", schema.VarChar(255), schema.Unique(), schema.Comment("Unique login address")),
	schema.Column("name", schema.Text, schema.Nullable()),
	schema.Column("created_at", schema.Timestamp, schema.Default("now()")),
)

// Posts reference the user that wrote them.
var Posts = Public.Table("posts",
	schema.Column("id", schema.BigInt, schema.PrimaryKey(), schema.Default()),
	schema.Column("user_id", schema.BigInt, schema.References(Users, "id")),
	schema.Column("title", schema.Text),
	schema.Column("body", schema.Text, schema.Nullable()),
	schema.Column("published", schema.Bool, schema.Default("false")),
	schema.Index("user_id"),
	schema.Check("title <> ''"),
)
