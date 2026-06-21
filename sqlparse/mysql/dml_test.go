package mysql_test

import (
	"testing"

	"github.com/aita/sqlkit/sql"
)

func TestInsertValues(t *testing.T) {
	assertRoundTrip(t,
		"INSERT INTO users (email, name) VALUES ('a@x.com', 'Alice')",
		sql.InsertInto(sql.Table{Name: sql.NewIdent("users")}).Columns("email", "name").Values("a@x.com", "Alice"),
		"INSERT INTO `users` (`email`, `name`) VALUES (?, ?)",
		"a@x.com", "Alice")
}

func TestInsertMultipleRows(t *testing.T) {
	assertRoundTrip(t,
		"INSERT INTO t (a, b) VALUES (1, 2), (3, 4)",
		sql.InsertInto(sql.Table{Name: sql.NewIdent("t")}).Columns("a", "b").Values(1, 2).Values(3, 4),
		"INSERT INTO `t` (`a`, `b`) VALUES (?, ?), (?, ?)",
		1, 2, 3, 4)
}

func TestInsertFromSelect(t *testing.T) {
	assertRoundTrip(t,
		"INSERT INTO archive (id) SELECT id FROM users",
		sql.InsertInto(sql.Table{Name: sql.NewIdent("archive")}).Columns("id").
			FromSelect(sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("users")})),
		"INSERT INTO `archive` (`id`) SELECT `id` FROM `users`")
}

func TestInsertOnDuplicateKeyUpdate(t *testing.T) {
	assertRoundTrip(t,
		"INSERT INTO users (email, name) VALUES ('a@x.com', 'Alice') ON DUPLICATE KEY UPDATE name = VALUES(name)",
		sql.InsertInto(sql.Table{Name: sql.NewIdent("users")}).Columns("email", "name").Values("a@x.com", "Alice").
			OnConflict().DoUpdate(sql.Set("name", sql.Excluded("name"))),
		"INSERT INTO `users` (`email`, `name`) VALUES (?, ?) ON DUPLICATE KEY UPDATE `name` = VALUES(`name`)",
		"a@x.com", "Alice")
}

func TestUpdate(t *testing.T) {
	assertRoundTrip(t,
		"UPDATE users SET email = 'b@x.com', name = 'Bob' WHERE id = 1",
		sql.Update(sql.Table{Name: sql.NewIdent("users")}).
			Set(sql.Set("email", "b@x.com"), sql.Set("name", "Bob")).
			Where(sql.Eq(sql.Col("id"), 1)),
		"UPDATE `users` SET `email` = ?, `name` = ? WHERE (`id` = ?)",
		"b@x.com", "Bob", 1)
}

func TestDelete(t *testing.T) {
	assertRoundTrip(t,
		"DELETE FROM users WHERE id = 1",
		sql.DeleteFrom(sql.Table{Name: sql.NewIdent("users")}).Where(sql.Eq(sql.Col("id"), 1)),
		"DELETE FROM `users` WHERE (`id` = ?)",
		1)
}

func TestUpdateMultiTable(t *testing.T) {
	assertRoundTrip(t,
		"UPDATE orders JOIN users u ON orders.uid = u.id SET orders.vip = 1 WHERE u.tier = 'gold'",
		sql.Update(sql.Table{Name: sql.NewIdent("orders")}).
			Join(sql.SourceAlias(sql.Table{Name: sql.NewIdent("users")}, "u"), sql.Eq(sql.Col("orders.uid"), sql.Col("u.id"))).
			Set(sql.Set("orders.vip", 1)).
			Where(sql.Eq(sql.Col("u.tier"), "gold")),
		"UPDATE `orders` JOIN `users` AS `u` ON (`orders`.`uid` = `u`.`id`) SET `orders`.`vip` = ? WHERE (`u`.`tier` = ?)",
		1, "gold")
}

func TestReplaceInto(t *testing.T) {
	assertRoundTrip(t,
		"REPLACE INTO users (id, name) VALUES (1, 'Alice')",
		sql.ReplaceInto(sql.Table{Name: sql.NewIdent("users")}).Columns("id", "name").Values(1, "Alice"),
		"REPLACE INTO `users` (`id`, `name`) VALUES (?, ?)",
		1, "Alice")
}

func TestUpdateOrderByLimit(t *testing.T) {
	assertRoundTrip(t,
		"UPDATE users SET active = 0 WHERE last_seen < 100 ORDER BY last_seen LIMIT 10",
		sql.Update(sql.Table{Name: sql.NewIdent("users")}).Set(sql.Set("active", 0)).
			Where(sql.Lt(sql.Col("last_seen"), 100)).
			OrderBy(sql.Asc(sql.Col("last_seen"))).Limit(10),
		"UPDATE `users` SET `active` = ? WHERE (`last_seen` < ?) ORDER BY `last_seen` LIMIT 10",
		0, 100)
}

func TestDeleteOrderByLimit(t *testing.T) {
	assertRoundTrip(t,
		"DELETE FROM logs WHERE level = 'debug' ORDER BY created_at LIMIT 100",
		sql.DeleteFrom(sql.Table{Name: sql.NewIdent("logs")}).
			Where(sql.Eq(sql.Col("level"), "debug")).
			OrderBy(sql.Asc(sql.Col("created_at"))).Limit(100),
		"DELETE FROM `logs` WHERE (`level` = ?) ORDER BY `created_at` LIMIT 100",
		"debug")
}

func TestDeleteMultiTable(t *testing.T) {
	assertRoundTrip(t,
		"DELETE orders FROM orders JOIN users u ON orders.uid = u.id WHERE u.banned = 1",
		sql.DeleteFrom(sql.Table{Name: sql.NewIdent("orders")}).
			Join(sql.SourceAlias(sql.Table{Name: sql.NewIdent("users")}, "u"), sql.Eq(sql.Col("orders.uid"), sql.Col("u.id"))).
			Where(sql.Eq(sql.Col("u.banned"), 1)),
		"DELETE `orders` FROM `orders` JOIN `users` AS `u` ON (`orders`.`uid` = `u`.`id`) WHERE (`u`.`banned` = ?)",
		1)
}

func TestUpdateWithCTE(t *testing.T) {
	assertRoundTrip(t,
		"WITH vip AS (SELECT id FROM users WHERE tier = 'gold') UPDATE orders SET priority = 1 WHERE uid IN (SELECT id FROM vip)",
		sql.Update(sql.Table{Name: sql.NewIdent("orders")}).Set(sql.Set("priority", 1)).
			Where(sql.InQuery(sql.Col("uid"), sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("vip")}))).
			WithCTE(sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("users")}).
				Where(sql.Eq(sql.Col("tier"), "gold")).CTE("vip")),
		"WITH `vip` AS (SELECT `id` FROM `users` WHERE (`tier` = ?)) "+
			"UPDATE `orders` SET `priority` = ? WHERE (`uid` IN (SELECT `id` FROM `vip`))",
		"gold", 1)
}

func TestUpdateMultiTableLeftJoin(t *testing.T) {
	assertRoundTrip(t,
		"UPDATE orders LEFT JOIN users u ON orders.uid = u.id SET orders.flag = 1",
		sql.Update(sql.Table{Name: sql.NewIdent("orders")}).
			LeftJoin(sql.SourceAlias(sql.Table{Name: sql.NewIdent("users")}, "u"), sql.Eq(sql.Col("orders.uid"), sql.Col("u.id"))).
			Set(sql.Set("orders.flag", 1)),
		"UPDATE `orders` LEFT JOIN `users` AS `u` ON (`orders`.`uid` = `u`.`id`) SET `orders`.`flag` = ?",
		1)
}

// A NATURAL/USING join in a multi-table UPDATE has no typed home and round-trips
// raw.
func TestUpdateMultiTableNaturalJoinFallsBackToRaw(t *testing.T) {
	assertRaw(t, "UPDATE orders NATURAL JOIN users SET orders.flag = 1")
}

func TestDeleteUsing(t *testing.T) {
	// DELETE t FROM t, u (a comma/CROSS join) maps to DeleteFrom(t).Using(u).
	assertRoundTrip(t,
		"DELETE logs FROM logs, archived WHERE logs.id = archived.id",
		sql.DeleteFrom(sql.Table{Name: sql.NewIdent("logs")}).
			Using(sql.Table{Name: sql.NewIdent("archived")}).
			Where(sql.Eq(sql.Col("logs.id"), sql.Col("archived.id"))),
		"DELETE `logs` FROM `logs`, `archived` WHERE (`logs`.`id` = `archived`.`id`)")
}

func TestDMLUnmodeledFallsBackToRaw(t *testing.T) {
	// Multi-target DELETE and an aliased multi-table base have no typed home.
	assertRaw(t, "DELETE a, b FROM a JOIN b ON a.id = b.aid")
	assertRaw(t, "UPDATE orders o JOIN users u ON o.uid = u.id SET o.vip = 1")
}

// RETURNING clauses on INSERT/UPDATE/DELETE have no typed home and round-trip raw.
func TestDMLReturningFallsBackToRaw(t *testing.T) {
	assertRaw(t, "INSERT INTO t (a) VALUES (1) RETURNING a")
	assertRaw(t, "DELETE FROM t WHERE id = 1 RETURNING id")
}

// A DELETE whose single target is distinct from the FROM base has no typed home.
func TestDeleteTargetDistinctFromBaseFallsBackToRaw(t *testing.T) {
	assertRaw(t, "DELETE users FROM orders JOIN users ON orders.uid = users.id")
}
