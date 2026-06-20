// This is a separate module so the SQL parser frontends' (pg_query_go cgo and
// the TiDB parser) dependency trees do not reach the root sqlkit module that
// library consumers import. It exists purely to host the round-trip oracle,
// which needs both the builder (root) and the parsers in one place.
module github.com/aita/sqlkit/internal/tests/roundtrip

go 1.25.0

require (
	github.com/aita/sqlkit v0.0.0
	github.com/aita/sqlkit/sqlparse/mysql v0.0.0
	github.com/aita/sqlkit/sqlparse/postgres v0.0.0
)

require (
	github.com/coreos/go-semver v0.3.1 // indirect
	github.com/pganalyze/pg_query_go/v6 v6.2.2 // indirect
	github.com/pingcap/errors v0.11.5-0.20250523034308-74f78ae071ee // indirect
	github.com/pingcap/failpoint v0.0.0-20240528011301-b51a646c7c86 // indirect
	github.com/pingcap/log v1.1.0 // indirect
	github.com/pingcap/tidb/pkg/parser v0.0.0-20260612105733-b1d4142f99a6 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	golang.org/x/text v0.19.0 // indirect
	google.golang.org/protobuf v1.31.0 // indirect
	gopkg.in/natefinch/lumberjack.v2 v2.2.1 // indirect
)

replace github.com/aita/sqlkit => ../../..

replace github.com/aita/sqlkit/sqlparse/postgres => ../../../sqlparse/postgres

replace github.com/aita/sqlkit/sqlparse/mysql => ../../../sqlparse/mysql
