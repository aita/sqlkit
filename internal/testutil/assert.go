package testutil

import (
	"fmt"
	"go/format"
	"testing"
)

// AssertValidMigrationBody wraps a rendered migration body in a scaffold and
// gofmts it, so a malformed op.* call surfaces as a syntax error rather than
// slipping through. It is shared by the autogen unit tests (migrate/autogen) and
// the end-to-end integration tests, which live in different packages and so
// cannot share a test-local helper.
func AssertValidMigrationBody(t *testing.T, body string) {
	t.Helper()
	src := fmt.Sprintf(`package migrations

import (
	"github.com/aita/sqlkit/migrate"
	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/decl"
)

var _ = func(op *migrate.Operator) {
%s
}

var (
	_ = sql.Raw
	_ = decl.BigInt
)
`, body)
	if _, err := format.Source([]byte(src)); err != nil {
		t.Fatalf("rendered body is not valid Go: %v\n--- src ---\n%s", err, src)
	}
}
