//go:build !sqlparser_rs

package sqlparser

// This stub is compiled when the `sqlparser_rs` build tag is absent, keeping
// sqlkit buildable as pure Go without a Rust toolchain. The real implementation
// lives in bridge.go.

// Parse always returns ErrUnavailable in the stub build. Rebuild with
// `-tags sqlparser_rs` (and a prebuilt Rust static library) to enable parsing.
func Parse(dialect, sql string) ([]Statement, error) {
	return nil, ErrUnavailable
}

// Available reports whether the cgo binding is compiled in. It is false in the
// stub build.
func Available() bool { return false }
