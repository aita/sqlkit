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

	if !cfg.Enabled {
		t.Error("Enabled = false, want true by default")
	}
	if _, ok := cfg.Dialect.(sql.PostgreSQLDialect); !ok {
		t.Errorf("Dialect = %T, want sql.PostgreSQLDialect by default", cfg.Dialect)
	}
	if cfg.Parser != nil {
		t.Errorf("Parser = %v, want nil by default", cfg.Parser)
	}
	// Defaults are production-minded: no raw SQL, callers only on error.
	if cfg.CaptureRawSQL {
		t.Error("CaptureRawSQL = true, want false by default")
	}
	if cfg.CaptureCaller != CallerOnError {
		t.Errorf("CaptureCaller = %v, want CallerOnError by default", cfg.CaptureCaller)
	}
	if !cfg.Fingerprint || !cfg.AnalyzeSQL || !cfg.NPlusOne.Enabled {
		t.Error("Fingerprint/AnalyzeSQL/NPlusOne should be on by default")
	}
}

func TestWithDialectAndParser(t *testing.T) {
	cfg := NewConfig(WithDialect(sql.MySQLDialect{}), WithParser(stubParser{}))
	if _, ok := cfg.Dialect.(sql.MySQLDialect); !ok {
		t.Errorf("Dialect = %T, want sql.MySQLDialect", cfg.Dialect)
	}
	if _, ok := cfg.Parser.(stubParser); !ok {
		t.Errorf("Parser = %T, want stubParser", cfg.Parser)
	}
}

func TestWithDialectNilKeepsDefault(t *testing.T) {
	cfg := NewConfig(WithDialect(nil))
	if _, ok := cfg.Dialect.(sql.PostgreSQLDialect); !ok {
		t.Errorf("Dialect = %T, want PostgreSQL default for nil", cfg.Dialect)
	}
}

func TestWithVerboseCapture(t *testing.T) {
	cfg := NewConfig(WithVerboseCapture())
	if !cfg.CaptureRawSQL {
		t.Error("WithVerboseCapture should turn on CaptureRawSQL")
	}
	if cfg.CaptureCaller != CallerAlways {
		t.Errorf("CaptureCaller = %v, want CallerAlways", cfg.CaptureCaller)
	}
}

func TestConfigOptionsCompose(t *testing.T) {
	cfg := NewConfig(
		WithRawSQL(),
		WithArgCapture(ArgsOff),
		WithCaller(CallerOnSlowQuery),
		WithSlowQueryThreshold(250*time.Millisecond),
		WithNPlusOne(NPlusOneConfig{Enabled: true, MinCount: 3}),
	)
	if !cfg.CaptureRawSQL {
		t.Error("WithRawSQL did not apply")
	}
	if cfg.CaptureArgs != ArgsOff {
		t.Errorf("CaptureArgs = %v, want ArgsOff", cfg.CaptureArgs)
	}
	if cfg.CaptureCaller != CallerOnSlowQuery {
		t.Errorf("CaptureCaller = %v, want CallerOnSlowQuery", cfg.CaptureCaller)
	}
	if cfg.SlowQueryThreshold != 250*time.Millisecond {
		t.Errorf("SlowQueryThreshold = %v, want 250ms", cfg.SlowQueryThreshold)
	}
	if cfg.NPlusOne.MinCount != 3 {
		t.Errorf("NPlusOne.MinCount = %d, want 3", cfg.NPlusOne.MinCount)
	}
}
