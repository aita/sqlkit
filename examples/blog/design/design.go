// Package design declares the blog schema with the sqlkit dsl. It is the
// in-code equivalent of ../schema.sql; regenerate ../blogdb from it with:
//
//	go run ./gen
package design

import "github.com/aita/sqlkit/dsl"

// Blog mirrors schema.sql: users and the posts that reference them.
var Blog = dsl.Schema("public", func() {
	dsl.Table("users", func() {
		dsl.Column("id", dsl.BigInt, dsl.PrimaryKey(), dsl.Default())
		dsl.Column("email", dsl.VarChar(255))
		dsl.Column("name", dsl.Text, dsl.Nullable())
		dsl.Column("created_at", dsl.Timestamp, dsl.Default())
	})
	dsl.Table("posts", func() {
		dsl.Column("id", dsl.BigInt, dsl.PrimaryKey(), dsl.Default())
		dsl.Column("user_id", dsl.BigInt, dsl.References("users", "id"))
		dsl.Column("title", dsl.Text)
		dsl.Column("body", dsl.Text, dsl.Nullable())
		dsl.Column("published", dsl.Bool, dsl.Default())
	})
})
