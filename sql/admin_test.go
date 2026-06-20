package sql

import (
	"strings"
	"testing"
)

func TestMySQLReplaceInto(t *testing.T) {
	replace := ReplaceInto(Table{Name: NewIdent("users")}).Columns("id", "name").Values(1, "Alice")
	assertMySQL(t, replace, "REPLACE INTO `users` (`id`, `name`) VALUES (?, ?)", []any{1, "Alice"})

	if _, _, err := replace.ToSQL(PostgreSQLDialect{}); err == nil ||
		!strings.Contains(err.Error(), "REPLACE INTO") {
		t.Fatalf("ToSQL(postgres) error = %v, want REPLACE rejection", err)
	}
}

func TestMySQLUpdateDeleteOrderLimit(t *testing.T) {
	update := Update(Table{Name: NewIdent("t")}).Set(Set("a", 1)).OrderBy(Asc(Col("id"))).Limit(5)
	assertMySQL(t, update, "UPDATE `t` SET `a` = ? ORDER BY `id` LIMIT 5", []any{1})

	del := DeleteFrom(Table{Name: NewIdent("t")}).OrderBy(Desc(Col("id"))).Limit(3)
	assertMySQL(t, del, "DELETE FROM `t` ORDER BY `id` DESC LIMIT 3", nil)

	if _, _, err := del.ToSQL(PostgreSQLDialect{}); err == nil ||
		!strings.Contains(err.Error(), "ORDER BY / LIMIT") {
		t.Fatalf("ToSQL(postgres) error = %v, want ORDER BY/LIMIT rejection", err)
	}
}

func TestMySQLCreateTableIndexAndCollate(t *testing.T) {
	ct := CreateTable(Table{Name: NewIdent("t")}).
		Column(ColumnDef{Name: NewIdent("name"), Type: RawType("varchar(20)"), Collate: NewIdent("utf8mb4_bin")}).
		Index("idx_name", "name")
	want := "CREATE TABLE `t` (\n" +
		"    `name` varchar(20) COLLATE utf8mb4_bin,\n" +
		"    KEY `idx_name` (`name`)\n" +
		");"
	assertMySQL(t, ct, want, nil)

	if _, _, err := ct.ToSQL(PostgreSQLDialect{}); err == nil ||
		!strings.Contains(err.Error(), "inline table indexes") {
		t.Fatalf("ToSQL(postgres) error = %v, want inline-index rejection", err)
	}
}

func TestMySQLAdminStatements(t *testing.T) {
	assertMySQL(t, Use("shop"), "USE `shop`;", nil)

	assertMySQL(t, LockTables(
		TableLock{Table: Table{Name: NewIdent("users")}, Mode: LockRead},
		TableLock{Table: Table{Name: NewIdent("orders")}, Mode: LockWrite},
	), "LOCK TABLES `users` READ, `orders` WRITE;", nil)

	assertMySQL(t, UnlockTables(), "UNLOCK TABLES;", nil)

	assertMySQL(t, SetVariables(
		SetVar{Name: "autocommit", Value: Raw("1"), Scope: "GLOBAL"},
		SetVar{Name: "x", Value: Raw("5"), User: true},
	), "SET GLOBAL autocommit = 1, @x = 5;", nil)
}
