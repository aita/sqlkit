// This is a separate module so the TiDB parser dependency tree (its decimal,
// charset and logging helpers) does not reach the root sqlkit module (which now
// includes the sqlparse package) that library consumers import.
module github.com/aita/sqlkit/sqlparse/mysql

go 1.25.0

require (
	github.com/aita/sqlkit v0.0.0
	github.com/pingcap/tidb/pkg/parser v0.0.0-20260612105733-b1d4142f99a6
)

require (
	github.com/coreos/go-semver v0.3.1 // indirect
	github.com/pingcap/errors v0.11.5-0.20250523034308-74f78ae071ee // indirect
	github.com/pingcap/failpoint v0.0.0-20240528011301-b51a646c7c86 // indirect
	github.com/pingcap/log v1.1.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	golang.org/x/text v0.19.0 // indirect
	gopkg.in/natefinch/lumberjack.v2 v2.2.1 // indirect
)

replace github.com/aita/sqlkit => ../..
