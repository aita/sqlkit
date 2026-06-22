// This is a separate module so the parser dependency tree (go-pgquery, wazero,
// and the pg_query_go AST types) does not reach the root sqlkit module (which
// now includes the sqlparse package) that library consumers import.
module github.com/aita/sqlkit/sqlparse/postgres

go 1.25.0

require (
	github.com/aita/sqlkit v0.0.0
	github.com/google/go-cmp v0.7.0
	github.com/pganalyze/pg_query_go/v6 v6.2.2
	github.com/wasilibs/go-pgquery v0.0.0-20260616022436-0b34969e40e5
)

require (
	github.com/tetratelabs/wazero v1.12.0 // indirect
	github.com/wasilibs/wazero-helpers v0.0.0-20250123031827-cd30c44769bb // indirect
	golang.org/x/sys v0.44.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/aita/sqlkit => ../..
