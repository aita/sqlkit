package querytrace

import (
	"testing"
	"time"

	"github.com/aita/sqlkit/sql"
)

// stubParser is a trivial Parser used to prove options store what they are given
// without depending on a real (cgo) frontend.
type stubParser struct{}

func (stubParser) Parse(string) ([]sql.Query, error) { return nil, nil }

func TestNewConfigDefaults(t *testing.T) {
	cfg := NewConfig()

	if !cfg.enabled {
		t.Error("enabled = false, want true by default")
	}
	if _, ok := cfg.dialect.(sql.PostgreSQLDialect); !ok {
		t.Errorf("dialect = %T, want sql.PostgreSQLDialect by default", cfg.dialect)
	}
	if cfg.parser != nil {
		t.Errorf("parser = %v, want nil by default", cfg.parser)
	}
	// Defaults are production-minded: no raw SQL, callers only on error.
	if cfg.captureRawSQL {
		t.Error("captureRawSQL = true, want false by default")
	}
	if cfg.captureCaller != CallerOnError {
		t.Errorf("captureCaller = %v, want CallerOnError by default", cfg.captureCaller)
	}
	if !cfg.fingerprint || !cfg.analyzeSQL || !cfg.nplusOne.enabled {
		t.Error("fingerprint/analyzeSQL/nplusOne should be on by default")
	}
}

func TestWithDialectAndParser(t *testing.T) {
	cfg := NewConfig(WithDialect(sql.MySQLDialect{}), WithParser(stubParser{}))
	if _, ok := cfg.dialect.(sql.MySQLDialect); !ok {
		t.Errorf("dialect = %T, want sql.MySQLDialect", cfg.dialect)
	}
	if _, ok := cfg.parser.(stubParser); !ok {
		t.Errorf("parser = %T, want stubParser", cfg.parser)
	}
}

func TestWithDialectNilKeepsDefault(t *testing.T) {
	cfg := NewConfig(WithDialect(nil))
	if _, ok := cfg.Dialect().(sql.PostgreSQLDialect); !ok {
		t.Errorf("Dialect() = %T, want PostgreSQL default for nil", cfg.Dialect())
	}
}

func TestWithVerboseCapture(t *testing.T) {
	cfg := NewConfig(WithVerboseCapture())
	if !cfg.captureRawSQL {
		t.Error("WithVerboseCapture should turn on captureRawSQL")
	}
	if cfg.captureCaller != CallerAlways {
		t.Errorf("captureCaller = %v, want CallerAlways", cfg.captureCaller)
	}
}

func TestConfigOptionsCompose(t *testing.T) {
	cfg := NewConfig(
		WithRawSQL(),
		WithArgCapture(ArgsOff),
		WithCaller(CallerOnSlowQuery),
		WithSlowQueryThreshold(250*time.Millisecond),
		WithNPlusOneThreshold(3),
	)
	if !cfg.captureRawSQL {
		t.Error("WithRawSQL did not apply")
	}
	if cfg.captureArgs != ArgsOff {
		t.Errorf("captureArgs = %v, want ArgsOff", cfg.captureArgs)
	}
	if cfg.captureCaller != CallerOnSlowQuery {
		t.Errorf("captureCaller = %v, want CallerOnSlowQuery", cfg.captureCaller)
	}
	if cfg.slowQueryThreshold != 250*time.Millisecond {
		t.Errorf("slowQueryThreshold = %v, want 250ms", cfg.slowQueryThreshold)
	}
	if cfg.nplusOne.minCount != 3 {
		t.Errorf("nplusOne.minCount = %d, want 3", cfg.nplusOne.minCount)
	}
}

func TestNPlusOneOptions(t *testing.T) {
	cfg := NewConfig(WithoutNPlusOne())
	if cfg.nplusOne.enabled {
		t.Error("WithoutNPlusOne should disable detection")
	}

	cfg = NewConfig(WithNPlusOneIgnore(StmtInsert, StmtUpdate))
	if len(cfg.nplusOne.ignoreOperations) != 2 {
		t.Errorf("ignoreOperations = %v, want two kinds", cfg.nplusOne.ignoreOperations)
	}

	// A non-positive threshold falls back to the default of 10.
	if got := NewConfig(WithNPlusOneThreshold(0)).nPlusOneMinCount(); got != 10 {
		t.Errorf("nPlusOneMinCount() = %d, want 10 for a zero threshold", got)
	}
}

func TestWithoutAnalyzeAndFingerprint(t *testing.T) {
	cfg := NewConfig(WithoutAnalyze(), WithoutFingerprint())
	if cfg.analyzeSQL {
		t.Error("WithoutAnalyze should turn off analyzeSQL")
	}
	if cfg.fingerprint {
		t.Error("WithoutFingerprint should turn off fingerprint")
	}
}
