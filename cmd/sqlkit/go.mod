// The sqlkit command is its own module so the database drivers it links
// (go-sql-driver/mysql and jackc/pgx, needed by `inspect` to open a live
// database) do not reach the root sqlkit module that library consumers import.
module github.com/aita/sqlkit/cmd/sqlkit

go 1.25.0

require (
	github.com/aita/sqlkit v0.0.0
	github.com/alecthomas/kong v1.15.0
	github.com/go-sql-driver/mysql v1.9.3
	github.com/jackc/pgx/v5 v5.10.0
)

require (
	filippo.io/edwards25519 v1.1.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/sync v0.19.0 // indirect
	golang.org/x/text v0.34.0 // indirect
)

replace github.com/aita/sqlkit => ../..
