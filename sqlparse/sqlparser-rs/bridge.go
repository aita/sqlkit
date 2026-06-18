//go:build sqlparser_rs

package sqlparser

/*
#cgo LDFLAGS: ${SRCDIR}/rust/target/release/libsqlkit_parserrs.a -lm -ldl -lpthread
#include <stdlib.h>

char* sqlkit_parse(const char* dialect, const char* sql);
void  sqlkit_string_free(char* ptr);
*/
import "C"

import (
	"unsafe"
)

// Parse parses sql using the named dialect (see the Dialect* constants; an
// empty string means generic) and returns the parsed statements in
// sqlparser-rs's native AST shape as raw JSON. A SQL syntax error is returned
// as a *ParseError.
func Parse(dialect, sql string) ([]Statement, error) {
	cDialect := C.CString(dialect)
	defer C.free(unsafe.Pointer(cDialect))
	cSQL := C.CString(sql)
	defer C.free(unsafe.Pointer(cSQL))

	out := C.sqlkit_parse(cDialect, cSQL)
	if out == nil {
		return nil, &ParseError{Message: "bridge returned null"}
	}
	defer C.sqlkit_string_free(out)

	raw := C.GoString(out)
	return decodeEnvelope([]byte(raw))
}

// Available reports whether the cgo binding is compiled in. It is true in this
// build; the stub build returns false.
func Available() bool { return true }
