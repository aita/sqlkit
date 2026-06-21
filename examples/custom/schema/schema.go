// Package schema declares the catalog the custom example generates from,
// as a decl.Catalog. It is the same blog schema as examples/generated (users
// authored posts and own at most one profile), defined separately here so the
// two examples can be read side by side: generated runs it through the CLI with
// the default type mapping, while main.go here generates from the same shape
// with a customized mapper, so the bindings come out differently.
//
// To give the custom mapper something to show, the users table carries a few
// extra richly-typed columns (a uuid, a numeric, a jsonb, and a status) that the
// plain blog schema in examples/generated does not — the two schemas need not
// match exactly. The column types here are still plain SQL types; the mapping to
// the blogdb domain types (UUID, Decimal, json.RawMessage, UserStatus) lives in
// the type mapper in main.go, not here.
package schema

import (
	"github.com/aita/sqlkit/decl"
	"github.com/aita/sqlkit/sql"
)

// Catalog holds the example catalog.
var Catalog = decl.NewCatalog()

// Public is the PostgreSQL schema the tables live in.
var Public = Catalog.Schema("public")

// Users are the registered blog authors.
var Users = Public.Table("users",
	decl.TableComment("Registered blog authors"),
	decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
	decl.Column("email", decl.VarChar(255), decl.Unique(), decl.Comment("Unique login address")),
	decl.Column("name", decl.Text, decl.Nullable()),
	// A status column the mapper maps to a typed Go enum via OverrideFunc.
	decl.Column("status", decl.VarChar(32), decl.Default(sql.Raw("'active'"))),
	// A money amount: numeric defaults to string. The mapper maps every numeric
	// column to blogdb.Decimal via Overrides.
	decl.Column("balance", decl.Decimal(18, 2), decl.Default(sql.Raw("0"))),
	// A free-form JSON blob: jsonb defaults to []byte. The mapper maps it to
	// json.RawMessage via ColumnOverrides.
	decl.Column("metadata", decl.JSON, decl.Default(sql.Raw("'{}'"))),
	// By default codegen maps uuid -> string. The mapper pins every uuid column
	// to the blogdb.UUID domain type via Overrides.
	decl.Column("external_id", decl.UUID, decl.Default(sql.Func("gen_random_uuid"))),
	decl.Column("created_at", decl.Timestamp, decl.Default(sql.Now())),
)

// Posts reference the user that wrote them (has-many).
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
