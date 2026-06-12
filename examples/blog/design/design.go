// Package design declares the blog schema with the sqlkit dsl, which is
// dot-imported goa-style so the schema reads as bare Schema/Table/Column
// calls. It is the in-code equivalent of ../schema.sql; regenerate ../blogdb
// from it with:
//
//	go run ./gen
package design

import . "github.com/aita/sqlkit/dsl"

// Blog mirrors schema.sql: users and the posts that reference them.
var Blog = Schema("public", func() {
	Table("users", func() {
		Column("id", BigInt, PrimaryKey(), Default())
		Column("email", VarChar(255))
		Column("name", Text, Nullable())
		Column("created_at", Timestamp, Default())
	})
	Table("posts", func() {
		Column("id", BigInt, PrimaryKey(), Default())
		Column("user_id", BigInt, References("users", "id"))
		Column("title", Text)
		Column("body", Text, Nullable())
		Column("published", Bool, Default())
	})
})
