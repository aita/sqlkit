package mysql_test

import (
	"testing"

	"github.com/aita/sqlkit/sql"
)

func TestUse(t *testing.T) {
	assertRoundTrip(t, "USE shop",
		sql.Use("shop"),
		"USE `shop`;")
}

func TestLockTables(t *testing.T) {
	assertRoundTrip(t,
		"LOCK TABLES users READ, orders WRITE",
		sql.LockTables(
			sql.TableLock{Table: sql.Table{Name: "users"}, Mode: sql.LockRead},
			sql.TableLock{Table: sql.Table{Name: "orders"}, Mode: sql.LockWrite},
		),
		"LOCK TABLES `users` READ, `orders` WRITE;")
}

func TestUnlockTables(t *testing.T) {
	assertRoundTrip(t, "UNLOCK TABLES",
		sql.UnlockTables(),
		"UNLOCK TABLES;")
}

// SET, SHOW and LOAD DATA have irregular or unbounded grammars that no typed
// builder models, so they round-trip as verbatim RawQuery.
func TestAdminUnmodeledFallsBackToRaw(t *testing.T) {
	assertRaw(t, "SET autocommit = 1")
	assertRaw(t, "SET NAMES utf8mb4")
	assertRaw(t, "SHOW TABLES")
	assertRaw(t, "LOAD DATA INFILE 'f' INTO TABLE t")
}
