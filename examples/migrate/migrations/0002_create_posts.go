package migrations

import (
	"github.com/aita/sqlkit/decl"
	"github.com/aita/sqlkit/migrate"
)

var _ = Registry.Add(migrate.Migration{
	Revision:     "0002",
	DownRevision: "0001",
	Name:         "create_posts",
	Up: func(op *migrate.Operator) {
		op.CreateTable("posts",
			decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
			decl.Column("user_id", decl.BigInt),
			decl.Column("title", decl.Text),
			decl.Index("user_id"),
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
