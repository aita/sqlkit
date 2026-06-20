package postgres_test

import (
	"testing"

	"github.com/aita/sqlkit/sql"
)

func TestCreateSequenceBare(t *testing.T) {
	assertRoundTrip(t,
		"CREATE SEQUENCE IF NOT EXISTS order_id_seq",
		sql.CreateSequence(sql.Table{Name: sql.NewIdent("order_id_seq")}).IfNotExists(),
		`CREATE SEQUENCE IF NOT EXISTS "order_id_seq";`)
}

func TestCreateSequenceOptions(t *testing.T) {
	assertRoundTrip(t,
		"CREATE SEQUENCE s INCREMENT BY 2 MINVALUE 1 NO MAXVALUE START WITH 5 CACHE 10 CYCLE OWNED BY t.id",
		sql.CreateSequence(sql.Table{Name: sql.NewIdent("s")}).
			Increment(2).MinValue(1).NoMaxValue().Start(5).Cache(10).Cycle().OwnedBy("t.id"),
		`CREATE SEQUENCE "s" INCREMENT BY 2 MINVALUE 1 NO MAXVALUE START WITH 5 CACHE 10 CYCLE OWNED BY "t"."id";`)
}

func TestDropSequenceIfExists(t *testing.T) {
	assertRoundTrip(t,
		"DROP SEQUENCE IF EXISTS s1, s2 CASCADE",
		sql.DropSequence("s1", "s2").IfExists().Cascade(),
		`DROP SEQUENCE IF EXISTS "s1", "s2" CASCADE;`)
}

// The AS data-type option is not modeled, so the statement round-trips raw.
func TestCreateSequenceAsTypeFallsBackToRaw(t *testing.T) {
	assertRaw(t, "CREATE SEQUENCE s AS smallint")
}
