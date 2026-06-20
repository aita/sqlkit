// This is a separate module so the rqlite/sql parser dependency tree does not
// reach the root sqlkit module (which includes the sqlparse package) that
// library consumers import. Unlike the postgres frontend this dependency is
// pure Go, so the module stays cgo-free.
module github.com/aita/sqlkit/sqlparse/sqlite

go 1.25.0

require (
	github.com/aita/sqlkit v0.0.0
	github.com/rqlite/sql v0.0.0-20260224021119-1b2524a41372
)

require github.com/google/go-cmp v0.7.0

replace github.com/aita/sqlkit => ../..
