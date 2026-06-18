// A separate module so the Rust/cgo dependency (the sqlparser-rs static
// library, linked under the `sqlparser_rs` build tag) does not reach the root
// sqlkit module nor the parent sqlparse module that library consumers import.
//
// This module returns sqlparser-rs's native AST as JSON and depends on nothing
// but the standard library, so it can be used on its own. Mapping that AST onto
// sqlkit/sql (implementing sqlparse.Parser) is layered on separately.
module github.com/aita/sqlkit/x/sqlparse/sqlparser-rs

go 1.25.0
