package querytrace

import (
	"testing"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse"
)

func TestDialectByName(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"postgres", "postgres"},
		{"mysql", "mysql"},
		{"sqlite", "sqlite"},
		{"", "postgres"},       // empty defaults to postgres
		{"oracle", "postgres"}, // unknown defaults to postgres
	}
	for _, tt := range tests {
		if got := dialectByName(tt.name).Name(); got != tt.want {
			t.Errorf("dialectByName(%q).Name() = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// regStubParser is a trivial Parser registered to prove DebugConfig wires the
// parser from its dialect name without depending on a real (cgo) frontend.
type regStubParser struct{}

func (regStubParser) Parse(string) ([]sql.Query, error) { return nil, nil }

func TestDebugConfigWiresParserFromDialect(t *testing.T) {
	// Register a stub under the mysql dialect name (no real mysql frontend is
	// imported in this test binary) and confirm DebugConfig wires it by the
	// resolved dialect's name.
	sqlparse.Register("mysql", func() sqlparse.Parser { return regStubParser{} })

	cfg := DebugConfig("mysql")
	if _, ok := cfg.Parser.(regStubParser); !ok {
		t.Fatalf("DebugConfig did not wire the registered parser; got %T", cfg.Parser)
	}
}

func TestDebugConfigStoresDialectStruct(t *testing.T) {
	cfg := DebugConfig("mysql")
	if _, ok := cfg.Dialect.(sql.MySQLDialect); !ok {
		t.Fatalf("Dialect = %T, want sql.MySQLDialect", cfg.Dialect)
	}
}

func TestDebugConfigNoFrontendLeavesParserNil(t *testing.T) {
	// No frontend registered for "sqlite" in this test binary, so Parser stays
	// nil and analysis will fall back to string heuristics.
	cfg := DebugConfig("sqlite")
	if cfg.Parser != nil {
		t.Fatalf("Parser = %v, want nil without a registered frontend", cfg.Parser)
	}
}
