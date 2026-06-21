// Command querytrace is its own module so the cgo PostgreSQL parser
// (pg_query_go) it links stays out of the main examples module, which is pure
// Go.
module github.com/aita/sqlkit/examples/querytrace

go 1.25.0

require (
	github.com/aita/sqlkit v0.0.0
	github.com/aita/sqlkit/sqlparse/postgres v0.0.0
	github.com/jackc/pgx/v5 v5.10.0
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/pganalyze/pg_query_go/v6 v6.2.2 // indirect
	golang.org/x/sync v0.17.0 // indirect
	golang.org/x/text v0.29.0 // indirect
	google.golang.org/protobuf v1.31.0 // indirect
)

replace github.com/aita/sqlkit => ../..

replace github.com/aita/sqlkit/sqlparse/postgres => ../../sqlparse/postgres
