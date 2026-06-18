// Package integration exercises sqlkit end to end against real PostgreSQL and
// MySQL servers. The same tests run on both engines so the query builder, the
// scanner, the schema DDL generator, and the introspection are all verified against
// the databases they target.
//
// It lives in its own module so the heavy testcontainers dependency tree stays
// out of the root sqlkit module that library consumers import. Run it from this
// directory:
//
//	cd internal/integration && go test ./...
//
// Each engine connects one of two ways:
//
//   - If SQLKIT_TEST_POSTGRES_DSN / SQLKIT_TEST_MYSQL_DSN is set, the suite
//     connects to that DSN. CI sets these to point at service containers, which
//     is fast and needs no Docker socket inside the job.
//   - Otherwise the suite boots a throwaway container with testcontainers,
//     which needs a working Docker daemon. When neither a DSN nor Docker is
//     available the engine's tests skip rather than fail.
//
// The MySQL DSN must enable parseTime so DATETIME columns scan into time.Time,
// and must select a database named "public" because the shared blog bindings
// qualify tables with the "public" schema (a PostgreSQL schema maps to a MySQL
// database).
package integration

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/testcontainers/testcontainers-go"
	tcmysql "github.com/testcontainers/testcontainers-go/modules/mysql"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/dbtest"
	"github.com/aita/sqlkit/meta"
)

// engine describes one database backend the suite runs against.
type engine struct {
	name    string         // ddl/introspection dialect name: "postgres" or "mysql"
	driver  string         // database/sql driver name passed to sqlkit.Open
	dialect sqlkit.Dialect // dialect used for identifier quoting in the harness
	// dsnEnv is the environment variable holding a ready-to-use DSN; when empty
	// the harness boots a container instead.
	dsnEnv string
	// supportsReturning reports whether INSERT/UPDATE/DELETE ... RETURNING is
	// available; MySQL has no RETURNING, so those tests fall back to
	// LastInsertId or are skipped.
	supportsReturning bool
}

var engines = []engine{
	{
		name:              "postgres",
		driver:            "pgx",
		dialect:           sqlkit.PostgreSQLDialect{},
		dsnEnv:            "SQLKIT_TEST_POSTGRES_DSN",
		supportsReturning: true,
	},
	{
		name:              "mysql",
		driver:            "mysql",
		dialect:           sqlkit.MySQLDialect{},
		dsnEnv:            "SQLKIT_TEST_MYSQL_DSN",
		supportsReturning: false,
	},
}

// backend caches a single shared connection per engine. Booting a container or
// opening a pool is expensive, so it happens once; tests get isolation from
// applySchema, which drops and recreates the tables for every subtest.
type backend struct {
	once sync.Once
	db   *sqlkit.Database
	skip string
	err  error
}

var backends = func() map[string]*backend {
	m := map[string]*backend{}
	for _, eng := range engines {
		m[eng.name] = &backend{}
	}
	return m
}()

// eachEngine runs fn as a subtest against every engine, with a freshly created
// schema. Engines without a reachable backend are skipped, not failed.
func eachEngine(t *testing.T, fn func(t *testing.T, eng engine, db *sqlkit.Database)) {
	t.Helper()
	for _, eng := range engines {
		t.Run(eng.name, func(t *testing.T) {
			db := setup(t, eng)
			fn(t, eng, db)
		})
	}
}

// setup returns the shared database for the engine after recreating the schema.
func setup(t *testing.T, eng engine) *sqlkit.Database {
	t.Helper()
	db := connect(t, eng)
	applySchema(t, db, eng)
	return db
}

// connect returns the engine's shared database, opening it on first use. It
// skips the test when the backend is unavailable and fails on a genuine error.
func connect(t *testing.T, eng engine) *sqlkit.Database {
	t.Helper()
	b := backends[eng.name]
	b.once.Do(func() { b.init(eng) })
	if b.skip != "" {
		t.Skip(b.skip)
	}
	if b.err != nil {
		t.Fatalf("connect %s: %v", eng.name, b.err)
	}
	return b.db
}

func (b *backend) init(eng engine) {
	ctx := context.Background()
	dsn := os.Getenv(eng.dsnEnv)
	if dsn == "" {
		started, err := startContainer(ctx, eng)
		if err != nil {
			b.skip = fmt.Sprintf("%s backend unavailable: set %s or start Docker (%v)", eng.name, eng.dsnEnv, err)
			return
		}
		dsn = started
	}
	db, err := sqlkit.Open(eng.driver, dsn)
	if err != nil {
		b.err = err
		return
	}
	if err := waitReady(ctx, db); err != nil {
		b.err = err
		return
	}
	b.db = db
}

// waitReady blocks until the pool can serve a query or the deadline passes,
// reporting the last ping error so a persistent failure (bad DSN, auth) is
// diagnosable rather than a bare timeout.
func waitReady(ctx context.Context, db *sqlkit.Database) error {
	deadline := time.Now().Add(30 * time.Second)
	for {
		err := db.SQL().PingContext(ctx)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("database never became reachable: %w", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// startContainer boots a throwaway database container and returns its DSN.
// Containers are reaped by testcontainers' Ryuk when the test process exits, so
// the shared connection does not terminate them explicitly.
func startContainer(ctx context.Context, eng engine) (string, error) {
	switch eng.name {
	case "postgres":
		container, err := tcpostgres.Run(ctx, image("SQLKIT_TEST_POSTGRES_IMAGE", "postgres:16-alpine"),
			tcpostgres.WithDatabase("blog"),
			tcpostgres.WithUsername("sqlkit"),
			tcpostgres.WithPassword("sqlkit"),
			testcontainers.WithWaitStrategy(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).
					WithStartupTimeout(60*time.Second)),
		)
		if err != nil {
			return "", err
		}
		return container.ConnectionString(ctx, "sslmode=disable")
	case "mysql":
		container, err := tcmysql.Run(ctx, image("SQLKIT_TEST_MYSQL_IMAGE", "mysql:8.0"),
			tcmysql.WithDatabase("public"),
			tcmysql.WithUsername("sqlkit"),
			tcmysql.WithPassword("sqlkit"),
		)
		if err != nil {
			return "", err
		}
		// tls=skip-verify makes the driver use the server's auto-generated
		// certificate, so caching_sha2_password auth completes over TLS without
		// the cleartext public-key retrieval handshake.
		return container.ConnectionString(ctx, "parseTime=true", "tls=skip-verify")
	default:
		return "", fmt.Errorf("no container recipe for engine %q", eng.name)
	}
}

func image(envVar, fallback string) string {
	if v := os.Getenv(envVar); v != "" {
		return v
	}
	return fallback
}

// applySchema recreates the shared schema (testCatalog) for the engine,
// delegating to dbtest.ApplySchema: it drops the tables in reverse declaration
// order (children before parents, so foreign keys never block the drop) and
// executes the CREATE SQL the meta model renders for the database's dialect.
// Running it per subtest gives each test a clean database. eng is unused now
// that dbtest reads the dialect from the database handle.
func applySchema(t *testing.T, db *sqlkit.Database, eng engine) {
	t.Helper()
	md, err := testCatalog.Build()
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	dbtest.ApplySchema(t, db, md)
}

func qualifiedName(t meta.Table) string {
	if t.Schema.Name == "" {
		return t.Name
	}
	return t.Schema.Name + "." + t.Name
}

// splitStatements breaks rendered DDL into individual statements. The generated
// DDL never contains a semicolon inside a string literal or identifier, so a
// plain split on ";" is sufficient and keeps the harness dependency-free.
func splitStatements(ddlText string) []string {
	var out []string
	for _, part := range strings.Split(ddlText, ";") {
		if stmt := strings.TrimSpace(part); stmt != "" {
			out = append(out, stmt)
		}
	}
	return out
}

func ptr[T any](v T) *T { return &v }
