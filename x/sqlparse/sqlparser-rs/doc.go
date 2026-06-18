// Package sqlparser is a cgo binding over sqlparser-rs (the Rust `sqlparser`
// crate). It parses SQL text with a chosen dialect and returns sqlparser-rs's
// own AST as JSON.
//
// It is a self-contained Go module
// (github.com/aita/sqlkit/x/sqlparse/sqlparser-rs) that depends on nothing but
// the standard library, so it can be used on its own. It lives in its own
// module — rather than inside the parent sqlparse module — so its Rust/cgo
// dependency does not reach consumers of sqlparse. Mapping this native AST onto
// sqlkit's sql.* types (implementing sqlparse.Parser) is layered on separately.
//
// # Usage
//
//	stmts, err := sqlparser.Parse(sqlparser.DialectPostgreSQL, "SELECT 1")
//
// The dialect selects the grammar; see the Dialect constants and Dialects.
//
// # Build
//
// The binding links a Rust static library that must be built first:
//
//	go generate .      # in this module's directory
//
// or equivalently `make`. This produces rust/target/release/libsqlkit_parserrs.a,
// which cgo links statically.
//
// Because it relies on cgo and a prebuilt Rust artifact, parsing is gated behind
// the `sqlparser_rs` build tag, so the module builds as pure Go without a Rust
// toolchain. Enable the real parser with:
//
//	go build -tags sqlparser_rs ./...
//	go test  -tags sqlparser_rs ./...
//
// Without the tag, a stub implementation is compiled whose Parse returns
// ErrUnavailable and Available reports false.
//
//go:generate make
package sqlparser
