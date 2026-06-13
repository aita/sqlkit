package migrations

import (
	"github.com/aita/sqlkit/migrate"
	"github.com/aita/sqlkit/sqldef"
)

var _ = Registry.Add(migrate.Migration{
	Revision:     "0002",
	DownRevision: "0001",
	Name:         "create_posts",
	Up: func(op *migrate.Operator) {
		op.CreateTable("posts",
			sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
			sqldef.Column("user_id", sqldef.BigInt),
			sqldef.Column("title", sqldef.Text),
			sqldef.Index("user_id"),
		)
		// Foreign keys reference their target table by name.
		op.CreateForeignKey("fk_posts_user_id", "posts",
			[]string{"user_id"}, "users", []string{"id"})
	},
	Down: func(op *migrate.Operator) {
		op.DropForeignKey("fk_posts_user_id", "posts")
		op.DropTable("posts")
	},
})
