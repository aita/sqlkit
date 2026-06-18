// This is a separate module so the pg_query_go (cgo) dependency tree does not
// reach the root sqlkit module (which now includes the sqlparse package) that
// library consumers import.
module github.com/aita/sqlkit/sqlparse/postgres

go 1.25.0

require (
	github.com/aita/sqlkit v0.0.0
	github.com/pganalyze/pg_query_go/v6 v6.2.2
)

require google.golang.org/protobuf v1.31.0 // indirect

replace github.com/aita/sqlkit => ../..
