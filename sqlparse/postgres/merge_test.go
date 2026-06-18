package postgres_test

import (
	"testing"

	"github.com/aita/sqlkit/sql"
)

func TestMergeUpdateInsert(t *testing.T) {
	assertRoundTrip(t,
		"MERGE INTO accounts AS t USING staging AS s ON t.id = s.id "+
			"WHEN MATCHED THEN UPDATE SET balance = s.balance "+
			"WHEN NOT MATCHED THEN INSERT (id, balance) VALUES (s.id, s.balance)",
		sql.MergeInto(sql.Table{Name: "accounts"}).As("t").
			Using(sql.SourceAlias(sql.Table{Name: "staging"}, "s")).
			On(sql.Eq(sql.Col("t.id"), sql.Col("s.id"))).
			When(sql.WhenMatched().ThenUpdate(sql.Set("balance", sql.Col("s.balance")))).
			When(sql.WhenNotMatched().ThenInsert([]string{"id", "balance"}, sql.Col("s.id"), sql.Col("s.balance"))),
		`MERGE INTO "accounts" AS "t" USING "staging" AS "s" ON ("t"."id" = "s"."id") `+
			`WHEN MATCHED THEN UPDATE SET "balance" = "s"."balance" `+
			`WHEN NOT MATCHED THEN INSERT ("id", "balance") VALUES ("s"."id", "s"."balance")`)
}

func TestMergeConditionDeleteDoNothing(t *testing.T) {
	assertRoundTrip(t,
		"MERGE INTO t USING src AS s ON t.k = s.k "+
			"WHEN MATCHED AND s.n > 0 THEN DELETE "+
			"WHEN MATCHED THEN DO NOTHING "+
			"WHEN NOT MATCHED BY SOURCE THEN DELETE",
		sql.MergeInto(sql.Table{Name: "t"}).
			Using(sql.SourceAlias(sql.Table{Name: "src"}, "s")).
			On(sql.Eq(sql.Col("t.k"), sql.Col("s.k"))).
			When(sql.WhenMatched().And(sql.Gt(sql.Col("s.n"), 0)).ThenDelete()).
			When(sql.WhenMatched().ThenDoNothing()).
			When(sql.WhenNotMatchedBySource().ThenDelete()),
		`MERGE INTO "t" USING "src" AS "s" ON ("t"."k" = "s"."k") `+
			`WHEN MATCHED AND ("s"."n" > $1) THEN DELETE `+
			`WHEN MATCHED THEN DO NOTHING `+
			`WHEN NOT MATCHED BY SOURCE THEN DELETE`,
		0)
}
