package migrations

import (
	"github.com/aita/sqlkit/decl"
	"github.com/aita/sqlkit/migrate"
)

var _ = Registry.Add(migrate.Migration{
	Revision:     "0003",
	DownRevision: "0002",
	Name:         "add_user_name",
	Up: func(op *migrate.Operator) {
		op.AddColumn("users", decl.Column("name", decl.Text, decl.Nullable()))
	},
	Down: func(op *migrate.Operator) {
		op.DropColumn("users", "name")
	},
})
