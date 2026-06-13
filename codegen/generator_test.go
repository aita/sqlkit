package codegen

import (
	"strings"
	"testing"
)

func TestGeneratorIDStableAndStructured(t *testing.T) {
	id := GeneratorID()
	if id != GeneratorID() {
		t.Fatal("GeneratorID is not deterministic")
	}
	// fmt<format>+tpl<hash>+sqlkit<version>
	for _, part := range []string{"fmt" + formatVersion + "+", "+tpl", "+sqlkit"} {
		if !strings.Contains(id, part) {
			t.Fatalf("GeneratorID %q missing %q", id, part)
		}
	}
}

func TestSqlkitVersionFallsBackToDevel(t *testing.T) {
	// Under `go test` of the main module the version is unversioned, so the
	// fingerprint reports the devel fallback rather than an empty string.
	if got := sqlkitVersion(); got == "" {
		t.Fatal("sqlkitVersion returned empty; want a version or (devel)")
	}
}
