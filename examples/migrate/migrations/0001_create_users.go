package migrations

import (
	"github.com/aita/sqlkit/decl"
	"github.com/aita/sqlkit/migrate"
	"github.com/aita/sqlkit/sql"
)

var _ = Registry.Add(migrate.Migration{
	Revision:     "0001",
	DownRevision: "", // first migration
	Name:         "create_users",
	Up: func(op *migrate.Operator) {
		op.CreateTable("users",
			decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
			decl.Column("email", decl.VarChar(255), decl.Unique()),
			decl.Column("created_at", decl.Timestamp, decl.Default(sql.Now())),
		)
	},
	Down: func(op *migrate.Operator) {
		op.DropTable("users")
	},
})
