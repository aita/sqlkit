package sqlparse_test

import (
	"sync"
	"testing"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse"
)

// stubParser is a trivial Parser used to exercise the registry without a real
// frontend's dependencies.
type stubParser struct{ name string }

func (stubParser) Parse(string) ([]sql.Query, error) { return nil, nil }

func TestRegisterAndFor(t *testing.T) {
	sqlparse.Register("registry_test_dialect", func() sqlparse.Parser {
		return stubParser{name: "registry_test_dialect"}
	})

	p, ok := sqlparse.For("registry_test_dialect")
	if !ok {
		t.Fatal("For: registered dialect not found")
	}
	if got, want := p.(stubParser).name, "registry_test_dialect"; got != want {
		t.Fatalf("For returned %q, want %q", got, want)
	}
}

func TestForUnregistered(t *testing.T) {
	if p, ok := sqlparse.For("no_such_dialect"); ok || p != nil {
		t.Fatalf("For(unregistered) = (%v, %v), want (nil, false)", p, ok)
	}
}

func TestRegisterLastWins(t *testing.T) {
	sqlparse.Register("registry_test_overwrite", func() sqlparse.Parser { return stubParser{name: "first"} })
	sqlparse.Register("registry_test_overwrite", func() sqlparse.Parser { return stubParser{name: "second"} })

	p, ok := sqlparse.For("registry_test_overwrite")
	if !ok {
		t.Fatal("For: overwritten dialect not found")
	}
	if got := p.(stubParser).name; got != "second" {
		t.Fatalf("For returned %q, want the most recently registered %q", got, "second")
	}
}

func TestRegisterNilFactoryPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Register(nil) did not panic")
		}
	}()
	sqlparse.Register("registry_test_nil", nil)
}

// TestRegistryConcurrent exercises Register/For under the race detector.
func TestRegistryConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			sqlparse.Register("registry_test_race", func() sqlparse.Parser { return stubParser{} })
		}()
		go func() { defer wg.Done(); sqlparse.For("registry_test_race") }()
	}
	wg.Wait()
}
