module github.com/aita/sqlkit/x/querytrace

go 1.25.0

require (
	github.com/aita/sqlkit v0.0.0
	github.com/aita/sqlkit/x/sqlparse v0.0.0
)

replace github.com/aita/sqlkit => ../..

replace github.com/aita/sqlkit/x/sqlparse => ../sqlparse
