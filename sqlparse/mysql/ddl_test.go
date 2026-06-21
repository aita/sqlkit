package mysql_test

import (
	"testing"

	"github.com/aita/sqlkit/sql"
)

func TestCreateTable(t *testing.T) {
	assertRoundTrip(t,
		"CREATE TABLE users (id BIGINT AUTO_INCREMENT PRIMARY KEY, email VARCHAR(255) NOT NULL UNIQUE, age INT DEFAULT 0)",
		sql.CreateTable(sql.Table{Name: sql.NewIdent("users")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("id"), Type: sql.EngineType{Name: "bigint"}, AutoIncrement: true}).
			Column(sql.ColumnDef{Name: sql.NewIdent("email"), Type: sql.RawType("varchar(255)"), NotNull: true, Unique: true}).
			Column(sql.ColumnDef{Name: sql.NewIdent("age"), Type: sql.RawType("int(11)"), Default: sql.Raw("0")}).
			PrimaryKey("id"),
		"CREATE TABLE `users` (\n"+
			"    `id` bigint NOT NULL AUTO_INCREMENT,\n"+
			"    `email` varchar(255) NOT NULL UNIQUE,\n"+
			"    `age` int(11) DEFAULT 0,\n"+
			"    PRIMARY KEY (`id`)\n"+
			");")
}

func TestCreateTableIfNotExists(t *testing.T) {
	assertRoundTrip(t,
		"CREATE TABLE IF NOT EXISTS t (id INT)",
		sql.CreateTable(sql.Table{Name: sql.NewIdent("t")}).IfNotExists().
			Column(sql.ColumnDef{Name: sql.NewIdent("id"), Type: sql.RawType("int(11)")}),
		"CREATE TABLE IF NOT EXISTS `t` (\n    `id` int(11)\n);")
}

func TestCreateTableForeignKey(t *testing.T) {
	assertRoundTrip(t,
		"CREATE TABLE posts (id INT PRIMARY KEY, uid INT, "+
			"CONSTRAINT fk_user FOREIGN KEY (uid) REFERENCES users (id) ON DELETE CASCADE)",
		sql.CreateTable(sql.Table{Name: sql.NewIdent("posts")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("id"), Type: sql.RawType("int(11)")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("uid"), Type: sql.RawType("int(11)")}).
			ForeignKey(sql.ForeignKeyConstraint{
				Name: sql.NewIdent("fk_user"), Columns: sql.Idents("uid"),
				RefTable: sql.NewIdent("users"), RefColumns: sql.Idents("id"), OnDelete: "CASCADE",
			}).
			PrimaryKey("id"),
		"CREATE TABLE `posts` (\n"+
			"    `id` int(11),\n"+
			"    `uid` int(11),\n"+
			"    PRIMARY KEY (`id`),\n"+
			"    CONSTRAINT `fk_user` FOREIGN KEY (`uid`) REFERENCES `users` (`id`) ON DELETE CASCADE\n"+
			");")
}

func TestCreateTableUniqueConstraint(t *testing.T) {
	assertRoundTrip(t,
		"CREATE TABLE t (a INT, b INT, CONSTRAINT uq UNIQUE (a, b))",
		sql.CreateTable(sql.Table{Name: sql.NewIdent("t")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("a"), Type: sql.RawType("int(11)")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("b"), Type: sql.RawType("int(11)")}).
			Unique("uq", "a", "b"),
		"CREATE TABLE `t` (\n"+
			"    `a` int(11),\n"+
			"    `b` int(11),\n"+
			"    CONSTRAINT `uq` UNIQUE (`a`, `b`)\n"+
			");")
}

func TestAlterTableAddColumn(t *testing.T) {
	assertRoundTrip(t,
		"ALTER TABLE users ADD COLUMN bio TEXT",
		sql.AlterTable(sql.Table{Name: sql.NewIdent("users")}).
			AddColumn(sql.ColumnDef{Name: sql.NewIdent("bio"), Type: sql.RawType("text")}),
		"ALTER TABLE `users` ADD COLUMN `bio` text;")
}

func TestAlterTableDropColumn(t *testing.T) {
	assertRoundTrip(t,
		"ALTER TABLE users DROP COLUMN bio",
		sql.AlterTable(sql.Table{Name: sql.NewIdent("users")}).DropColumn("bio"),
		"ALTER TABLE `users` DROP COLUMN `bio`;")
}

func TestAlterTableModifyColumn(t *testing.T) {
	assertRoundTrip(t,
		"ALTER TABLE users MODIFY COLUMN age BIGINT NOT NULL",
		sql.AlterTable(sql.Table{Name: sql.NewIdent("users")}).
			RestateColumn(sql.ColumnDef{Name: sql.NewIdent("age"), Type: sql.RawType("bigint(20)"), NotNull: true}),
		"ALTER TABLE `users` MODIFY COLUMN `age` bigint(20) NOT NULL;")
}

func TestAlterTableChangeColumnNoRename(t *testing.T) {
	// CHANGE COLUMN with the same name is a plain restate (MODIFY COLUMN).
	assertRoundTrip(t,
		"ALTER TABLE users CHANGE COLUMN age age BIGINT",
		sql.AlterTable(sql.Table{Name: sql.NewIdent("users")}).
			RestateColumn(sql.ColumnDef{Name: sql.NewIdent("age"), Type: sql.RawType("bigint(20)")}),
		"ALTER TABLE `users` MODIFY COLUMN `age` bigint(20);")
}

func TestAlterTableChangeColumnRenameFallsBackToRaw(t *testing.T) {
	assertRaw(t, "ALTER TABLE users CHANGE COLUMN age years BIGINT")
}

func TestAlterTableRenameColumn(t *testing.T) {
	assertRoundTrip(t,
		"ALTER TABLE users RENAME COLUMN a TO b",
		sql.AlterTable(sql.Table{Name: sql.NewIdent("users")}).RenameColumn("a", "b"),
		"ALTER TABLE `users` RENAME COLUMN `a` TO `b`;")
}

func TestCreateTemporaryTable(t *testing.T) {
	assertRoundTrip(t,
		"CREATE TEMPORARY TABLE tmp (id INT)",
		sql.CreateTable(sql.Table{Name: sql.NewIdent("tmp")}).Temporary().
			Column(sql.ColumnDef{Name: sql.NewIdent("id"), Type: sql.RawType("int(11)")}),
		"CREATE TEMPORARY TABLE `tmp` (\n    `id` int(11)\n);")
}

func TestDropTable(t *testing.T) {
	assertRoundTrip(t,
		"DROP TABLE IF EXISTS users, sessions",
		sql.DropTable(sql.Table{Name: sql.NewIdent("users")}, sql.Table{Name: sql.NewIdent("sessions")}).IfExists(),
		"DROP TABLE IF EXISTS `users`, `sessions`;")
}

func TestTruncate(t *testing.T) {
	assertRoundTrip(t,
		"TRUNCATE TABLE logs",
		sql.Truncate(sql.Table{Name: sql.NewIdent("logs")}),
		"TRUNCATE TABLE `logs`;")
}

func TestCreateDatabase(t *testing.T) {
	assertRoundTrip(t,
		"CREATE DATABASE IF NOT EXISTS shop",
		sql.CreateSchema("shop").IfNotExists(),
		"CREATE SCHEMA IF NOT EXISTS `shop`;")
}

func TestDropDatabase(t *testing.T) {
	assertRoundTrip(t,
		"DROP DATABASE IF EXISTS shop",
		sql.DropSchema("shop").IfExists(),
		"DROP SCHEMA IF EXISTS `shop`;")
}

// An inline column-level REFERENCES clause records a single-column foreign key on
// the column itself, with its referential action.
func TestCreateTableInlineColumnReference(t *testing.T) {
	assertRoundTrip(t,
		"CREATE TABLE posts (id INT, uid INT REFERENCES users (id) ON DELETE SET NULL)",
		sql.CreateTable(sql.Table{Name: sql.NewIdent("posts")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("id"), Type: sql.RawType("int(11)")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("uid"), Type: sql.RawType("int(11)"), References: &sql.ColumnReference{
				Table:    sql.NewIdent("users"),
				Column:   sql.NewIdent("id"),
				OnDelete: "SET NULL",
			}}),
		"CREATE TABLE `posts` (\n"+
			"    `id` int(11),\n"+
			"    `uid` int(11) REFERENCES `users` (`id`) ON DELETE SET NULL\n"+
			");")
}

// A CHECK constraint with no explicit name renders its predicate verbatim (DDL
// constants are not bound), exercising the static-expression path.
func TestCreateTableCheckConstraint(t *testing.T) {
	assertRoundTrip(t,
		"CREATE TABLE t (age INT, CHECK (age >= 0))",
		sql.CreateTable(sql.Table{Name: sql.NewIdent("t")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("age"), Type: sql.RawType("int(11)")}).
			Check("", sql.BinaryExpression{Left: sql.Col("age"), Operator: ">=", Right: sql.Raw("0")}),
		"CREATE TABLE `t` (\n"+
			"    `age` int(11),\n"+
			"    CONSTRAINT `` CHECK ((`age` >= 0))\n"+
			");")
}

// A column COMMENT is a string literal read verbatim, and a string DEFAULT is
// escaped and rendered as a static SQL constant rather than a bind parameter.
func TestCreateTableColumnCommentAndStringDefault(t *testing.T) {
	assertRoundTrip(t,
		"CREATE TABLE t (state VARCHAR(10) DEFAULT 'it''s on' COMMENT 'the state')",
		sql.CreateTable(sql.Table{Name: sql.NewIdent("t")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("state"), Type: sql.RawType("varchar(10)"),
				Default: sql.Raw("'it''s on'"), Comment: "the state"}),
		"CREATE TABLE `t` (\n"+
			"    `state` varchar(10) DEFAULT 'it''s on' COMMENT 'the state'\n"+
			");")
}

// A CHECK predicate over a function call and column references exercises the DDL
// static-expression path (FuncCall, ColumnName, nested BinaryOperation), where
// constants render verbatim rather than as bind parameters.
func TestCreateTableCheckWithFunction(t *testing.T) {
	assertRoundTrip(t,
		"CREATE TABLE t (a INT, b INT, CONSTRAINT ck CHECK (a < ABS(b)))",
		sql.CreateTable(sql.Table{Name: sql.NewIdent("t")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("a"), Type: sql.RawType("int(11)")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("b"), Type: sql.RawType("int(11)")}).
			Check("ck", sql.BinaryExpression{
				Left:     sql.Col("a"),
				Operator: "<",
				Right:    sql.Func("ABS", sql.Col("b")),
			}),
		"CREATE TABLE `t` (\n"+
			"    `a` int(11),\n"+
			"    `b` int(11),\n"+
			"    CONSTRAINT `ck` CHECK ((`a` < ABS(`b`)))\n"+
			");")
}

// Numeric column DEFAULTs render as static SQL constants: an integer, a float, and
// an unsigned value too large for int64 keep their exact text.
func TestCreateTableNumericDefaults(t *testing.T) {
	assertRoundTrip(t,
		"CREATE TABLE t (a INT DEFAULT 0, b DOUBLE DEFAULT 1.5, c BIGINT UNSIGNED DEFAULT 18446744073709551615)",
		sql.CreateTable(sql.Table{Name: sql.NewIdent("t")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("a"), Type: sql.RawType("int(11)"), Default: sql.Raw("0")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("b"), Type: sql.RawType("double"), Default: sql.Raw("1.5")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("c"), Type: sql.RawType("bigint(20) unsigned"), Default: sql.Raw("18446744073709551615")}),
		"CREATE TABLE `t` (\n"+
			"    `a` int(11) DEFAULT 0,\n"+
			"    `b` double DEFAULT 1.5,\n"+
			"    `c` bigint(20) unsigned DEFAULT 18446744073709551615\n"+
			");")
}

func TestAlterTableAddPrimaryKey(t *testing.T) {
	assertRoundTrip(t,
		"ALTER TABLE t ADD PRIMARY KEY (id)",
		sql.AlterTable(sql.Table{Name: sql.NewIdent("t")}).AddPrimaryKey("id"),
		"ALTER TABLE `t` ADD PRIMARY KEY (`id`);")
}

func TestAlterTableAddUnique(t *testing.T) {
	assertRoundTrip(t,
		"ALTER TABLE t ADD CONSTRAINT uq UNIQUE (a, b)",
		sql.AlterTable(sql.Table{Name: sql.NewIdent("t")}).AddUnique("uq", "a", "b"),
		"ALTER TABLE `t` ADD CONSTRAINT `uq` UNIQUE (`a`, `b`);")
}

func TestAlterTableAddForeignKey(t *testing.T) {
	assertRoundTrip(t,
		"ALTER TABLE posts ADD CONSTRAINT fk FOREIGN KEY (uid) REFERENCES users (id)",
		sql.AlterTable(sql.Table{Name: sql.NewIdent("posts")}).AddForeignKey(sql.ForeignKeyConstraint{
			Name: sql.NewIdent("fk"), Columns: sql.Idents("uid"),
			RefTable: sql.NewIdent("users"), RefColumns: sql.Idents("id"),
		}),
		"ALTER TABLE `posts` ADD CONSTRAINT `fk` FOREIGN KEY (`uid`) REFERENCES `users` (`id`);")
}

func TestAlterTableAddCheck(t *testing.T) {
	assertRoundTrip(t,
		"ALTER TABLE t ADD CONSTRAINT ck CHECK (age > 0)",
		sql.AlterTable(sql.Table{Name: sql.NewIdent("t")}).
			AddCheck("ck", sql.BinaryExpression{Left: sql.Col("age"), Operator: ">", Right: sql.Raw("0")}),
		"ALTER TABLE `t` ADD CONSTRAINT `ck` CHECK ((`age` > 0));")
}

func TestAlterTableDropPrimaryKey(t *testing.T) {
	assertRoundTrip(t,
		"ALTER TABLE t DROP PRIMARY KEY",
		sql.AlterTable(sql.Table{Name: sql.NewIdent("t")}).DropPrimaryKey(),
		"ALTER TABLE `t` DROP PRIMARY KEY;")
}

func TestAlterTableRenameTo(t *testing.T) {
	assertRoundTrip(t,
		"ALTER TABLE old RENAME TO new",
		sql.AlterTable(sql.Table{Name: sql.NewIdent("old")}).RenameTo("new"),
		"ALTER TABLE `old` RENAME TO `new`;")
}

// SPATIAL/FULLTEXT indexes have no typed home and round-trip as a verbatim
// RawQuery, exercising the non-plain index-key-type path.
func TestCreateFulltextIndexFallsBackToRaw(t *testing.T) {
	assertRaw(t, "CREATE FULLTEXT INDEX ft ON docs (body)")
}

// DROP INDEX IF EXISTS has no typed home (sqlkit's DropIndex carries no IF EXISTS),
// so it round-trips raw.
func TestDropIndexIfExistsFallsBackToRaw(t *testing.T) {
	assertRaw(t, "DROP INDEX IF EXISTS idx ON t")
}

// A multi-pair RENAME TABLE and a cross-schema rename both exceed sqlkit's single
// rename and round-trip raw.
func TestRenameTableUnmodeledFallsBackToRaw(t *testing.T) {
	assertRaw(t, "RENAME TABLE a TO b, c TO d")
	assertRaw(t, "RENAME TABLE s1.a TO s2.b")
}

// CREATE TABLE ... LIKE, AS SELECT, and a multi-table DROP VIEW have no typed home.
func TestCreateTableLikeAndSelectFallBackToRaw(t *testing.T) {
	assertRaw(t, "CREATE TABLE copy LIKE original")
	assertRaw(t, "CREATE TABLE copy AS SELECT id FROM original")
	assertRaw(t, "DROP VIEW a, b")
}

// An ALTER TABLE action sqlkit does not model (here a combined drop plus add)
// round-trips raw rather than erroring.
func TestAlterTableUnmodeledFallsBackToRaw(t *testing.T) {
	assertRaw(t, "ALTER TABLE t DROP INDEX i, ADD COLUMN c INT")
}

func TestCreateTableWithIndex(t *testing.T) {
	assertRoundTrip(t,
		"CREATE TABLE t (name VARCHAR(20) COLLATE utf8mb4_bin, KEY idx_name (name))",
		sql.CreateTable(sql.Table{Name: sql.NewIdent("t")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("name"), Type: sql.RawType("varchar(20)"), Collate: sql.NewIdent("utf8mb4_bin")}).
			Index("idx_name", "name"),
		"CREATE TABLE `t` (\n"+
			"    `name` varchar(20) COLLATE utf8mb4_bin,\n"+
			"    KEY `idx_name` (`name`)\n"+
			");")
}

func TestCreateTableGeneratedColumn(t *testing.T) {
	assertRoundTrip(t,
		"CREATE TABLE t (a INT, b INT, total INT GENERATED ALWAYS AS (a + b) STORED)",
		sql.CreateTable(sql.Table{Name: sql.NewIdent("t")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("a"), Type: sql.RawType("int(11)")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("b"), Type: sql.RawType("int(11)")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("total"), Type: sql.RawType("int(11)"), Generated: &sql.GeneratedColumn{
				Expression: sql.BinaryExpression{Left: sql.Col("a"), Operator: "+", Right: sql.Col("b")},
				Stored:     true,
			}}),
		"CREATE TABLE `t` (\n"+
			"    `a` int(11),\n"+
			"    `b` int(11),\n"+
			"    `total` int(11) GENERATED ALWAYS AS ((`a` + `b`)) STORED\n"+
			");")
}

func TestCreateTableOnUpdate(t *testing.T) {
	assertRoundTrip(t,
		"CREATE TABLE t (id INT, updated TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP)",
		sql.CreateTable(sql.Table{Name: sql.NewIdent("t")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("id"), Type: sql.RawType("int(11)")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("updated"), Type: sql.RawType("timestamp"),
				Default:  sql.Func("CURRENT_TIMESTAMP"),
				OnUpdate: sql.Func("CURRENT_TIMESTAMP"),
			}),
		"CREATE TABLE `t` (\n"+
			"    `id` int(11),\n"+
			"    `updated` timestamp DEFAULT CURRENT_TIMESTAMP() ON UPDATE CURRENT_TIMESTAMP()\n"+
			");")
}

func TestDropView(t *testing.T) {
	assertRoundTrip(t,
		"DROP VIEW IF EXISTS active_users",
		sql.DropView(sql.Table{Name: sql.NewIdent("active_users")}).IfExists(),
		"DROP VIEW IF EXISTS `active_users`;")
}

func TestCreateIndex(t *testing.T) {
	assertRoundTrip(t,
		"CREATE INDEX idx_email ON users (email)",
		sql.CreateIndex("idx_email", sql.Table{Name: sql.NewIdent("users")}, "email"),
		"CREATE INDEX `idx_email` ON `users` (`email`);")
}

func TestCreateUniqueIndex(t *testing.T) {
	assertRoundTrip(t,
		"CREATE UNIQUE INDEX uq_ab ON t (a, b)",
		sql.CreateIndex("uq_ab", sql.Table{Name: sql.NewIdent("t")}, "a", "b").Unique(),
		"CREATE UNIQUE INDEX `uq_ab` ON `t` (`a`, `b`);")
}

func TestDropIndex(t *testing.T) {
	assertRoundTrip(t,
		"DROP INDEX idx_email ON users",
		sql.DropIndex("idx_email", sql.Table{Name: sql.NewIdent("users")}),
		"DROP INDEX `idx_email` ON `users`;")
}

func TestRenameTable(t *testing.T) {
	assertRoundTrip(t,
		"RENAME TABLE old_users TO users",
		sql.RenameTable(sql.Table{Name: sql.NewIdent("old_users")}, "users"),
		"ALTER TABLE `old_users` RENAME TO `users`;")
}

func TestAlterTableDropForeignKey(t *testing.T) {
	assertRoundTrip(t,
		"ALTER TABLE posts DROP FOREIGN KEY fk_user",
		sql.DropForeignKey(sql.Table{Name: sql.NewIdent("posts")}, "fk_user"),
		"ALTER TABLE `posts` DROP FOREIGN KEY `fk_user`;")
}

func TestAlterTableDropIndex(t *testing.T) {
	assertRoundTrip(t,
		"ALTER TABLE users DROP INDEX idx_email",
		sql.DropIndex("idx_email", sql.Table{Name: sql.NewIdent("users")}),
		"DROP INDEX `idx_email` ON `users`;")
}

// A prefix-length index key has no typed home, so the whole statement falls back
// to a verbatim RawQuery.
func TestCreateIndexPrefixFallsBackToRaw(t *testing.T) {
	assertRaw(t, "CREATE INDEX idx ON t (name(10))")
}

// CREATE TABLE engine/charset options would be dropped by the typed builder, so
// the statement round-trips raw instead.
func TestCreateTableOptionsFallBackToRaw(t *testing.T) {
	assertRaw(t, "CREATE TABLE t (id INT) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4")
}

// CHARACTER SET and ZEROFILL live on the field type and are dropped by the
// verbatim type text the compiler keeps, so a column carrying either round-trips
// as raw rather than silently losing the attribute. A bare COLLATE (no explicit
// charset) and the intrinsic "binary" charset of BLOB/VARBINARY stay typed.
func TestColumnCharsetFallsBackToRaw(t *testing.T) {
	assertRaw(t, "CREATE TABLE t (a VARCHAR(10) CHARACTER SET utf8)")
}

func TestColumnZerofillFallsBackToRaw(t *testing.T) {
	assertRaw(t, "CREATE TABLE t (a INT UNSIGNED ZEROFILL)")
}
