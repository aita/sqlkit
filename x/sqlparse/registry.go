package sqlparse

import "sync"

// registry maps a dialect name (matching sql.Dialect.Name(), e.g. "postgres")
// to a factory for that dialect's parser. Frontends populate it from init().
var registry sync.Map // map[string]func() Parser

// Register associates a parser factory with a dialect name (matching
// sql.Dialect.Name(), e.g. "postgres"). A frontend calls this from its init,
// so blank-importing the frontend makes its parser discoverable through For.
// Registering the same name twice keeps the most recent factory.
func Register(dialect string, factory func() Parser) {
	if factory == nil {
		panic("sqlparse: Register called with a nil factory")
	}
	registry.Store(dialect, factory)
}

// For returns a parser for the named dialect, or (nil, false) when no frontend
// has registered it — for example when the frontend package was not imported.
func For(dialect string) (Parser, bool) {
	v, ok := registry.Load(dialect)
	if !ok {
		return nil, false
	}
	return v.(func() Parser)(), true
}
