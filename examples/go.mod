// The examples are their own module so the pgx driver that the blog example
// links to open a live database does not reach the root sqlkit module that
// library consumers import.
module github.com/aita/sqlkit/examples

go 1.25.0

require (
	github.com/aita/sqlkit v0.0.0
	github.com/aita/sqlkit/sqlparse/postgres v0.0.0-00010101000000-000000000000
	github.com/alecthomas/kong v1.15.0
	github.com/jackc/pgx/v5 v5.10.0
)

require (
	filippo.io/edwards25519 v1.1.0 // indirect
	github.com/go-sql-driver/mysql v1.9.3 // indirect
)

require (
	github.com/aita/sqlkit/cmd/sqlkit v0.0.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/pganalyze/pg_query_go/v6 v6.2.2 // indirect
	github.com/tetratelabs/wazero v1.12.0 // indirect
	github.com/wasilibs/go-pgquery v0.0.0-20260616022436-0b34969e40e5 // indirect
	github.com/wasilibs/wazero-helpers v0.0.0-20250123031827-cd30c44769bb // indirect
	golang.org/x/sync v0.19.0 // indirect
	golang.org/x/sys v0.44.0 // indirect
	golang.org/x/text v0.34.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/aita/sqlkit => ..

replace github.com/aita/sqlkit/cmd/sqlkit => ../cmd/sqlkit

replace github.com/aita/sqlkit/sqlparse/postgres => ../sqlparse/postgres

replace github.com/aita/sqlkit/sqlparse => ../sqlparse

tool github.com/aita/sqlkit/cmd/sqlkit
