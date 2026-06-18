// Package sqlparser is a cgo binding over sqlparser-rs (the Rust `sqlparser`
// crate). It parses SQL text with a chosen dialect and returns sqlparser-rs's
// own AST as JSON — it does not depend on sqlkit's sql package. Mapping that
// AST onto sqlkit's sql.* builder types is layered on top of this package, in
// the parent sqlparse package.
//
// # Build
//
// The binding links a Rust static library that must be built first:
//
//	go generate ./sqlparse/sqlparser-rs
//
// or equivalently `make -C sqlparse/sqlparser-rs`. This produces
// rust/target/release/libsqlkit_parserrs.a, which cgo links statically.
//
// Because it relies on cgo and a prebuilt Rust artifact, the binding is gated
// behind the `sqlparser_rs` build tag so that the rest of sqlkit stays pure Go
// and `go build ./...` keeps working without a Rust toolchain. Enable it with:
//
//	go build -tags sqlparser_rs ./...
//	go test  -tags sqlparser_rs ./sqlparse/...
//
// Without the tag, a stub implementation is compiled whose functions return
// ErrUnavailable.
//
//go:generate make
package sqlparser
