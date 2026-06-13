package migrations

import (
	"github.com/aita/sqlkit/migrate"
	"github.com/aita/sqlkit/sqldef"
)

var _ = Registry.Add(migrate.Migration{
	Revision:     "0003",
	DownRevision: "0002",
	Name:         "add_user_name",
	Up: func(op *migrate.Operator) {
		op.AddColumn("users", sqldef.Column("name", sqldef.Text, sqldef.Nullable()))
	},
	Down: func(op *migrate.Operator) {
		op.DropColumn("users", "name")
	},
})
