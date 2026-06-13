package migrations

import (
	"github.com/aita/sqlkit/migrate"
	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqldef"
)

var _ = Registry.Add(migrate.Migration{
	Revision:     "0001",
	DownRevision: "", // first migration
	Name:         "create_users",
	Up: func(op *migrate.Operator) {
		op.CreateTable("users",
			sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
			sqldef.Column("email", sqldef.VarChar(255), sqldef.Unique()),
			sqldef.Column("created_at", sqldef.Timestamp, sqldef.Default(sql.Raw("now()"))),
		)
	},
	Down: func(op *migrate.Operator) {
		op.DropTable("users")
	},
})
