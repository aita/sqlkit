// Package integration exercises sqlkit against a real PostgreSQL instance
// started with testcontainers. It lives in its own module so the heavy
// testcontainers dependency tree stays out of the root sqlkit module. Run it
// from this directory:
//
//	cd internal/integration && go test ./...
//
// It requires a working Docker daemon and registry access to pull the
// postgres image.
package integration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/sqlkit-dev/sqlkit/examples/blog/blogdb"
	"github.com/sqlkit-dev/sqlkit/internal/codegen"
	"github.com/sqlkit-dev/sqlkit/internal/loader"
	"github.com/sqlkit-dev/sqlkit/sqlkit"
	"github.com/sqlkit-dev/sqlkit/sqlkit/sql"
)

func schemaPath(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve caller path")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "examples", "blog", "schema.sql")
}

// postgresImage is the image used for integration tests. Override it with the
// SQLKIT_TEST_POSTGRES_IMAGE environment variable to use a registry or mirror
// reachable from your environment (e.g. mirror.gcr.io/library/postgres:16-alpine
// to avoid Docker Hub's anonymous pull rate limit).
func postgresImage() string {
	if image := os.Getenv("SQLKIT_TEST_POSTGRES_IMAGE"); image != "" {
		return image
	}
	return "postgres:16-alpine"
}

// startPostgres boots a PostgreSQL container seeded with the blog schema and
// returns a connected sqlkit Database.
func startPostgres(t *testing.T) *sqlkit.Database {
	t.Helper()
	ctx := context.Background()

	container, err := postgres.Run(ctx, postgresImage(),
		postgres.WithDatabase("blog"),
		postgres.WithUsername("sqlkit"),
		postgres.WithPassword("sqlkit"),
		postgres.WithInitScripts(schemaPath(t)),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	db, err := sqlkit.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.SQL().Close() })

	// Wait until the pool can actually serve a query.
	deadline := time.Now().Add(30 * time.Second)
	for {
		if err := db.SQL().PingContext(ctx); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("database never became reachable")
		}
		time.Sleep(200 * time.Millisecond)
	}
	return db
}

func TestLoaderReadsRealSchema(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	model, err := loader.Load(ctx, db.SQL(), "postgres", "public")
	if err != nil {
		t.Fatalf("loader.Load: %v", err)
	}

	tables := map[string]bool{}
	for _, table := range model.Tables {
		tables[table.Name] = true
	}
	if !tables["users"] || !tables["posts"] {
		t.Fatalf("loaded tables = %v, want users and posts", tables)
	}

	var idPK, idDefault, emailNullable bool
	for _, table := range model.Tables {
		if table.Name != "users" {
			continue
		}
		for _, c := range table.Columns {
			switch c.Name {
			case "id":
				idPK = c.PrimaryKey
				idDefault = c.HasDefault // bigserial -> has default
			case "email":
				emailNullable = c.Nullable
			}
		}
	}
	if !idPK || !idDefault {
		t.Fatalf("users.id metadata: pk=%v default=%v, want both true", idPK, idDefault)
	}
	if emailNullable {
		t.Fatal("users.email should be NOT NULL")
	}

	// The generated code for the live schema must compile (gofmt + parse).
	if _, err := codegen.Generate(model, codegen.Options{PackageName: "blogdb"}); err != nil {
		t.Fatalf("codegen.Generate against live schema: %v", err)
	}
}

// userRow and postRow are test-defined DTOs; the generated package contains
// only the table bindings.
type userRow struct {
	ID        int64
	Email     string
	Name      *string
	CreatedAt time.Time
}

type postRow struct {
	UserID    int64
	Title     string
	Body      *string
	Published bool
}

func TestQueryRoundTrip(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	// INSERT ... RETURNING with database-generated id/created_at.
	var alice []userRow
	if err := db.Insert(blogdb.Users).Values(userRow{Email: "alice@example.com"}).
		Returning().
		All(ctx).Scan(&alice); err != nil {
		t.Fatalf("insert alice: %v", err)
	}
	if len(alice) != 1 || alice[0].ID == 0 || alice[0].CreatedAt.IsZero() {
		t.Fatalf("inserted user = %#v, want generated id and created_at", alice)
	}
	aliceID := alice[0].ID

	var bob []userRow
	if err := db.Insert(blogdb.Users).Values(userRow{Email: "bob@example.com"}).
		Returning().
		All(ctx).Scan(&bob); err != nil {
		t.Fatalf("insert bob: %v", err)
	}

	if _, err := db.Insert(blogdb.Posts).Values([]postRow{
		{UserID: aliceID, Title: "Hello", Published: true},
		{UserID: aliceID, Title: "Draft"},
	}).Exec(ctx); err != nil {
		t.Fatalf("insert posts: %v", err)
	}

	// Get by primary key, into a caller-defined dest.
	var got userRow
	if found, err := blogdb.Users.Get(ctx, db, aliceID, &got); err != nil || !found {
		t.Fatalf("Get(%d) found=%v err=%v", aliceID, found, err)
	}
	if got.Email != "alice@example.com" {
		t.Fatalf("Get email = %q", got.Email)
	}
	var missing userRow
	if found, err := blogdb.Users.Get(ctx, db, 999999, &missing); err != nil || found {
		t.Fatalf("Get(missing) found=%v err=%v; want false, nil", found, err)
	}

	// Typed SELECT + JOIN scanned into a caller-defined DTO.
	type PostByAuthor struct {
		Title  string
		Author string `sqlkit:"email"`
	}
	var rows []PostByAuthor
	if err := db.Select(blogdb.Posts.Title, blogdb.Users.Email).
		From(blogdb.Posts).
		Join(blogdb.Users, blogdb.Posts.UserID.EqExpr(blogdb.Users.ID)).
		OrderBy(blogdb.Posts.Title.Asc()).
		All(ctx).Scan(&rows); err != nil {
		t.Fatalf("join select: %v", err)
	}
	if len(rows) != 2 || rows[0].Title != "Draft" || rows[0].Author != "alice@example.com" {
		t.Fatalf("join rows = %#v", rows)
	}

	// COUNT over a filtered query.
	drafts, err := db.Select().From(blogdb.Posts).
		Where(blogdb.Posts.Published.Eq(false)).
		Count(ctx)
	if err != nil {
		t.Fatalf("count drafts: %v", err)
	}
	if drafts != 1 {
		t.Fatalf("drafts = %d, want 1", drafts)
	}

	// Window function projection scanned into an ad hoc struct.
	type Ranked struct {
		Title string
		RN    int64 `sqlkit:"rn"`
	}
	var ranked []Ranked
	if err := db.Select(
		blogdb.Posts.Title,
		sql.As(sql.RowNumber().Over().PartitionBy(blogdb.Posts.UserID).OrderBy(blogdb.Posts.Title.Asc()), "rn"),
	).From(blogdb.Posts).
		OrderBy(blogdb.Posts.Title.Asc()).
		All(ctx).Scan(&ranked); err != nil {
		t.Fatalf("window query: %v", err)
	}
	if len(ranked) != 2 || ranked[0].RN != 1 || ranked[1].RN != 2 {
		t.Fatalf("ranked = %#v", ranked)
	}

	// UNION of typed queries into scalar slice. A compound ORDER BY refers to
	// the combined result's output columns, not table-qualified ones.
	var emails []string
	if err := db.Select(blogdb.Users.Email).From(blogdb.Users).Where(blogdb.Users.ID.Eq(aliceID)).
		Union(db.Select(blogdb.Users.Email).From(blogdb.Users).Where(blogdb.Users.Email.Eq("bob@example.com"))).
		OrderBy(sql.Asc(sql.Col("email"))).
		All(ctx).Scan(&emails); err != nil {
		t.Fatalf("union: %v", err)
	}
	if len(emails) != 2 || emails[0] != "alice@example.com" || emails[1] != "bob@example.com" {
		t.Fatalf("union emails = %#v", emails)
	}

	// INSERT ... SELECT: copy alice's drafts as new published posts.
	if _, err := db.Insert(blogdb.Posts).
		Columns(blogdb.Posts.UserID, blogdb.Posts.Title, blogdb.Posts.Published).
		FromSelect(db.Select(blogdb.Posts.UserID, blogdb.Posts.Title, sql.Raw("true")).
			From(blogdb.Posts).
			Where(blogdb.Posts.Published.Eq(false))).
		Exec(ctx); err != nil {
		t.Fatalf("insert from select: %v", err)
	}
	total, err := db.Select().From(blogdb.Posts).Count(ctx)
	if err != nil || total != 3 {
		t.Fatalf("posts after INSERT...SELECT = %d, %v; want 3", total, err)
	}

	// INSERT from a UNION without a column list: PostgreSQL accepts the
	// parenthesized compound directly after the table name.
	if _, err := db.Insert(blogdb.Posts).
		FromSelect(db.Select(blogdb.Posts.ID, blogdb.Posts.UserID, blogdb.Posts.Title, blogdb.Posts.Body, blogdb.Posts.Published).
			From(blogdb.Posts).Where(blogdb.Posts.Title.Eq("never-matches")).
			UnionAll(db.Select(blogdb.Posts.ID, blogdb.Posts.UserID, blogdb.Posts.Title, blogdb.Posts.Body, blogdb.Posts.Published).
				From(blogdb.Posts).Where(blogdb.Posts.Title.Eq("never-matches")))).
		Exec(ctx); err != nil {
		t.Fatalf("insert from union: %v", err)
	}

	// DELETE ... RETURNING a specific column.
	var deletedIDs []int64
	if err := db.Delete(blogdb.Posts).
		Where(blogdb.Posts.Published.Eq(false)).
		Returning(blogdb.Posts.ID).
		All(ctx).Scan(&deletedIDs); err != nil {
		t.Fatalf("delete returning: %v", err)
	}
	if len(deletedIDs) != 1 {
		t.Fatalf("deleted ids = %#v, want exactly one draft", deletedIDs)
	}

	// UPDATE inside a callback session: committed on nil return.
	if err := db.WithSession(func(session *sqlkit.Session) error {
		_, err := session.Update(blogdb.Users).
			Set(blogdb.Users.Name.Set(ptr("Alice"))).
			Where(blogdb.Users.ID.Eq(aliceID)).
			Exec(ctx)
		return err
	}); err != nil {
		t.Fatalf("update in session: %v", err)
	}
	var after userRow
	if found, err := blogdb.Users.Get(ctx, db, aliceID, &after); err != nil || !found || after.Name == nil || *after.Name != "Alice" {
		t.Fatalf("after update = %#v, found=%v, %v", after, found, err)
	}

	// A callback returning an error rolls the transaction back.
	rollback := errors.New("rollback")
	if err := db.WithSession(func(session *sqlkit.Session) error {
		if _, err := session.Update(blogdb.Users).
			Set(blogdb.Users.Name.Set(ptr("Mallory"))).
			Where(blogdb.Users.ID.Eq(aliceID)).
			Exec(ctx); err != nil {
			return err
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatalf("WithSession error = %v, want rollback", err)
	}
	var unchanged userRow
	if found, err := blogdb.Users.Get(ctx, db, aliceID, &unchanged); err != nil || !found || unchanged.Name == nil || *unchanged.Name != "Alice" {
		t.Fatalf("after rolled-back update = %#v, found=%v, %v; want Alice intact", unchanged, found, err)
	}
}

func ptr[T any](v T) *T { return &v }
