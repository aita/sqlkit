package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/dbtest"
	"github.com/aita/sqlkit/examples/blog/blogdb"
	"github.com/aita/sqlkit/sql"
)

// userRow and postRow are test-defined DTOs; the generated blogdb package
// contains only the table bindings, so callers bring their own row types.
type userRow struct {
	ID        int64
	Email     string
	Name      *string
	CreatedAt time.Time
}

type postRow struct {
	ID        int64
	UserID    int64
	Title     string
	Body      *string
	Published bool
}

// insertUser inserts a user and returns its database-generated id. It delegates
// to dbtest.InsertID, which reads the key back with RETURNING where the engine
// supports it and LastInsertId otherwise. eng is unused now that the dialect
// split lives in dbtest, but kept so call sites read uniformly with the rest of
// the suite.
func insertUser(t *testing.T, db *sqlkit.Database, eng engine, u userRow) int64 {
	t.Helper()
	return dbtest.InsertID(t, db, blogdb.Users, u)
}

// seedBlog inserts two users and several posts, returning the author ids. It is
// the shared fixture most query tests build on.
func seedBlog(t *testing.T, db *sqlkit.Database, eng engine) (aliceID, bobID int64) {
	t.Helper()
	ctx := context.Background()
	aliceID = insertUser(t, db, eng, userRow{Email: "alice@example.com", Name: ptr("Alice")})
	bobID = insertUser(t, db, eng, userRow{Email: "bob@example.com"})
	if _, err := db.Insert(blogdb.Posts).Values([]postRow{
		{UserID: aliceID, Title: "Hello", Published: true},
		{UserID: aliceID, Title: "Draft", Published: false},
		{UserID: bobID, Title: "Bob's first", Published: true},
	}).Exec(ctx); err != nil {
		t.Fatalf("insert posts: %v", err)
	}
	return aliceID, bobID
}

func TestInsertAndLookup(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()
		aliceID, _ := seedBlog(t, db, eng)

		// Lookup by primary key into a caller-defined dest. The bindings are
		// metadata, not an ORM, so the destination type is chosen by the caller.
		var got userRow
		if err := db.Select().From(blogdb.Users).Where(blogdb.Users.ID.Eq(aliceID)).One(ctx).Scan(&got); err != nil {
			t.Fatalf("lookup alice: %v", err)
		}
		if got.Email != "alice@example.com" || got.Name == nil || *got.Name != "Alice" {
			t.Fatalf("alice = %#v", got)
		}

		// A missing row yields ErrNoRows.
		var missing userRow
		err := db.Select().From(blogdb.Users).Where(blogdb.Users.ID.Eq(999999)).First(ctx).Scan(&missing)
		if !errors.Is(err, sqlkit.ErrNoRows) {
			t.Fatalf("lookup(missing) = %v; want ErrNoRows", err)
		}

		// One over a non-unique predicate returns ErrTooManyRows without
		// reading past the second row.
		var dup userRow
		err = db.Select().From(blogdb.Users).Where(blogdb.Users.ID.Gt(0)).One(ctx).Scan(&dup)
		if !errors.Is(err, sqlkit.ErrTooManyRows) {
			t.Fatalf("One over two rows = %v; want ErrTooManyRows", err)
		}
	})
}

func TestJoins(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()
		seedBlog(t, db, eng)

		// INNER JOIN scanned into an ad hoc DTO with a `sqlkit` tag remapping a
		// column onto a differently named field.
		type postByAuthor struct {
			Title  string
			Author string `sqlkit:"email"`
		}
		var rows []postByAuthor
		if err := db.Select(blogdb.Posts.Title, blogdb.Users.Email).
			From(blogdb.Posts).
			Join(blogdb.Users, blogdb.Posts.UserID.EqExpr(blogdb.Users.ID)).
			OrderBy(blogdb.Posts.Title.Asc()).
			All(ctx).Scan(&rows); err != nil {
			t.Fatalf("inner join: %v", err)
		}
		if len(rows) != 3 {
			t.Fatalf("inner join rows = %#v, want 3", rows)
		}
		if rows[0].Title != "Bob's first" || rows[0].Author != "bob@example.com" {
			t.Fatalf("first join row = %#v", rows[0])
		}

		// LEFT JOIN: a user with no posts still appears, with NULL post columns.
		insertUser(t, db, eng, userRow{Email: "carol@example.com"})
		type authorPost struct {
			Email string
			Title *string
		}
		var left []authorPost
		if err := db.Select(blogdb.Users.Email, blogdb.Posts.Title).
			From(blogdb.Users).
			LeftJoin(blogdb.Posts, blogdb.Users.ID.EqExpr(blogdb.Posts.UserID)).
			Where(blogdb.Users.Email.Eq("carol@example.com")).
			All(ctx).Scan(&left); err != nil {
			t.Fatalf("left join: %v", err)
		}
		if len(left) != 1 || left[0].Title != nil {
			t.Fatalf("left join rows = %#v, want one row with NULL title", left)
		}
	})
}

func TestAggregates(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()
		aliceID, bobID := seedBlog(t, db, eng)

		// COUNT over a filtered query (executed as a subquery).
		published, err := db.Select().From(blogdb.Posts).
			Where(blogdb.Posts.Published.Eq(true)).
			Count(ctx)
		if err != nil {
			t.Fatalf("count published: %v", err)
		}
		if published != 2 {
			t.Fatalf("published = %d, want 2", published)
		}

		// GROUP BY with COUNT and a HAVING filter, scanned into a DTO.
		type authorCount struct {
			UserID int64 `sqlkit:"user_id"`
			N      int64 `sqlkit:"n"`
		}
		var counts []authorCount
		if err := db.Select(
			blogdb.Posts.UserID,
			sql.As(sql.CountAll(), "n"),
		).From(blogdb.Posts).
			GroupBy(blogdb.Posts.UserID).
			Having(sqlkit.Cond(sql.Gt(sql.CountAll(), sql.Int(1)))).
			OrderBy(blogdb.Posts.UserID.Asc()).
			All(ctx).Scan(&counts); err != nil {
			t.Fatalf("group by/having: %v", err)
		}
		if len(counts) != 1 || counts[0].UserID != aliceID || counts[0].N != 2 {
			t.Fatalf("counts = %#v, want only alice (%d) with 2", counts, aliceID)
		}
		_ = bobID
	})
}

func TestPredicates(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()
		seedBlog(t, db, eng)

		// IN over a value set.
		var titles []string
		if err := db.Select(blogdb.Posts.Title).From(blogdb.Posts).
			Where(blogdb.Posts.Title.In("Hello", "Draft", "missing")).
			OrderBy(blogdb.Posts.Title.Asc()).
			All(ctx).Scan(&titles); err != nil {
			t.Fatalf("IN: %v", err)
		}
		if len(titles) != 2 || titles[0] != "Draft" || titles[1] != "Hello" {
			t.Fatalf("IN titles = %#v", titles)
		}

		// LIKE pattern match.
		var likeTitles []string
		if err := db.Select(blogdb.Posts.Title).From(blogdb.Posts).
			Where(sqlkit.Like(blogdb.Posts.Title, "%first%")).
			All(ctx).Scan(&likeTitles); err != nil {
			t.Fatalf("LIKE: %v", err)
		}
		if len(likeTitles) != 1 || likeTitles[0] != "Bob's first" {
			t.Fatalf("LIKE titles = %#v", likeTitles)
		}

		// IS NULL / IS NOT NULL over the nullable name column.
		named, err := db.Select().From(blogdb.Users).Where(blogdb.Users.Name.IsNotNull()).Count(ctx)
		if err != nil {
			t.Fatalf("IS NOT NULL count: %v", err)
		}
		if named != 1 {
			t.Fatalf("named users = %d, want 1 (alice)", named)
		}
		anon, err := db.Select().From(blogdb.Users).Where(blogdb.Users.Name.IsNull()).Count(ctx)
		if err != nil {
			t.Fatalf("IS NULL count: %v", err)
		}
		if anon != 1 {
			t.Fatalf("anonymous users = %d, want 1 (bob)", anon)
		}
	})
}

func TestOrderingAndPagination(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()
		for i := 0; i < 5; i++ {
			insertUser(t, db, eng, userRow{Email: pageEmail(i)})
		}

		// Page returns a LIMIT/OFFSET window over an ordered result.
		var page []userRow
		if err := db.Select().From(blogdb.Users).
			OrderBy(blogdb.Users.Email.Asc()).
			Page(ctx, 2, 1).Scan(&page); err != nil {
			t.Fatalf("page: %v", err)
		}
		if len(page) != 2 || page[0].Email != pageEmail(1) || page[1].Email != pageEmail(2) {
			t.Fatalf("page = %#v", page)
		}

		// Descending order with an explicit limit.
		var desc []string
		if err := db.Select(blogdb.Users.Email).From(blogdb.Users).
			OrderBy(blogdb.Users.Email.Desc()).
			Limit(1).
			All(ctx).Scan(&desc); err != nil {
			t.Fatalf("desc: %v", err)
		}
		if len(desc) != 1 || desc[0] != pageEmail(4) {
			t.Fatalf("desc = %#v", desc)
		}
	})
}

func pageEmail(i int) string {
	return string(rune('a'+i)) + "@example.com"
}

func TestWindowFunctions(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()
		seedBlog(t, db, eng)

		// ROW_NUMBER partitioned by author, ordered by title.
		type ranked struct {
			Title string
			RN    int64 `sqlkit:"rn"`
		}
		var rows []ranked
		if err := db.Select(
			blogdb.Posts.Title,
			sql.As(sql.RowNumber().Over().
				PartitionBy(blogdb.Posts.UserID).
				OrderBy(blogdb.Posts.Title.Asc()), "rn"),
		).From(blogdb.Posts).
			OrderBy(blogdb.Posts.UserID.Asc(), blogdb.Posts.Title.Asc()).
			All(ctx).Scan(&rows); err != nil {
			t.Fatalf("window query: %v", err)
		}
		// Alice has two posts (Draft, Hello -> rn 1, 2); Bob has one (rn 1).
		if len(rows) != 3 {
			t.Fatalf("window rows = %#v, want 3", rows)
		}
		if rows[0].RN != 1 || rows[1].RN != 2 || rows[2].RN != 1 {
			t.Fatalf("row numbers = %#v", rows)
		}
	})
}

func TestUnion(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()
		aliceID, _ := seedBlog(t, db, eng)

		// UNION of two typed queries into a scalar slice. The compound ORDER BY
		// refers to the combined result's output column, not a table-qualified
		// one.
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

		// UNION ALL keeps duplicates.
		var all []string
		if err := db.Select(blogdb.Users.Email).From(blogdb.Users).Where(blogdb.Users.ID.Eq(aliceID)).
			UnionAll(db.Select(blogdb.Users.Email).From(blogdb.Users).Where(blogdb.Users.ID.Eq(aliceID))).
			All(ctx).Scan(&all); err != nil {
			t.Fatalf("union all: %v", err)
		}
		if len(all) != 2 {
			t.Fatalf("union all = %#v, want 2 duplicate rows", all)
		}
	})
}

func TestInsertFromSelect(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()
		seedBlog(t, db, eng)

		// INSERT ... SELECT: republish every draft as a new published post.
		if _, err := db.Insert(blogdb.Posts).
			Columns(blogdb.Posts.UserID, blogdb.Posts.Title, blogdb.Posts.Published).
			FromSelect(db.Select(blogdb.Posts.UserID, blogdb.Posts.Title, sql.Raw("true")).
				From(blogdb.Posts).
				Where(blogdb.Posts.Published.Eq(false))).
			Exec(ctx); err != nil {
			t.Fatalf("insert from select: %v", err)
		}
		total, err := db.Select().From(blogdb.Posts).Count(ctx)
		if err != nil {
			t.Fatalf("count after insert-select: %v", err)
		}
		if total != 4 {
			t.Fatalf("posts after INSERT...SELECT = %d, want 4", total)
		}
		published, err := db.Select().From(blogdb.Posts).Where(blogdb.Posts.Published.Eq(true)).Count(ctx)
		if err != nil {
			t.Fatalf("count published: %v", err)
		}
		if published != 3 {
			t.Fatalf("published after INSERT...SELECT = %d, want 3", published)
		}
	})
}

func TestUpdateAndDelete(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()
		aliceID, _ := seedBlog(t, db, eng)

		// UPDATE with a typed assignment.
		res, err := db.Update(blogdb.Users).
			Set(blogdb.Users.Name.Set(ptr("Alice Liddell"))).
			Where(blogdb.Users.ID.Eq(aliceID)).
			Exec(ctx)
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if n, err := res.RowsAffected(); err != nil || n != 1 {
			t.Fatalf("update rows affected = %d, %v; want 1", n, err)
		}
		var after userRow
		if err := db.Select().From(blogdb.Users).Where(blogdb.Users.ID.Eq(aliceID)).One(ctx).Scan(&after); err != nil {
			t.Fatalf("reload alice: %v", err)
		}
		if after.Name == nil || *after.Name != "Alice Liddell" {
			t.Fatalf("after update = %#v", after)
		}

		// DELETE the drafts.
		res, err = db.Delete(blogdb.Posts).Where(blogdb.Posts.Published.Eq(false)).Exec(ctx)
		if err != nil {
			t.Fatalf("delete: %v", err)
		}
		if n, err := res.RowsAffected(); err != nil || n != 1 {
			t.Fatalf("delete rows affected = %d, %v; want 1", n, err)
		}
		remaining, err := db.Select().From(blogdb.Posts).Count(ctx)
		if err != nil {
			t.Fatalf("count after delete: %v", err)
		}
		if remaining != 2 {
			t.Fatalf("posts after delete = %d, want 2", remaining)
		}
	})
}

func TestRecordsStreaming(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()
		seedBlog(t, db, eng)

		var seen []string
		for title, err := range sqlkit.Records[string](
			db.Select(blogdb.Posts.Title).From(blogdb.Posts).OrderBy(blogdb.Posts.Title.Asc()).All(ctx),
		) {
			if err != nil {
				t.Fatalf("stream: %v", err)
			}
			seen = append(seen, title)
		}
		if len(seen) != 3 || seen[0] != "Bob's first" {
			t.Fatalf("streamed titles = %#v", seen)
		}
	})
}

func TestRawSQL(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()
		seedBlog(t, db, eng)

		// A hand-written statement compiled through the builder so its
		// placeholder renders for the engine's dialect.
		query := sql.Select(blogdb.Users.Email).
			From(blogdb.Users).
			Where(sql.Eq(blogdb.Users.Email, sql.Val("bob@example.com")))
		var email string
		if err := db.RawSQL(ctx, query).Scan(&email); err != nil {
			t.Fatalf("raw sql: %v", err)
		}
		if email != "bob@example.com" {
			t.Fatalf("raw sql email = %q", email)
		}
	})
}

func TestTransactions(t *testing.T) {
	eachEngine(t, func(t *testing.T, eng engine, db *sqlkit.Database) {
		ctx := context.Background()
		aliceID, _ := seedBlog(t, db, eng)

		// A committed session persists its writes.
		if err := db.WithSession(func(session *sqlkit.Session) error {
			_, err := session.Update(blogdb.Users).
				Set(blogdb.Users.Name.Set(ptr("Committed"))).
				Where(blogdb.Users.ID.Eq(aliceID)).
				Exec(ctx)
			return err
		}); err != nil {
			t.Fatalf("commit session: %v", err)
		}
		if name := userName(t, db, aliceID); name != "Committed" {
			t.Fatalf("after commit name = %q, want Committed", name)
		}

		// A session returning an error rolls back every write. The in-transaction
		// name is captured for an assertion outside the callback, so a failed
		// check never short-circuits the rollback via t.Fatal's goroutine exit.
		rollback := errors.New("rollback")
		var inTxName string
		err := db.WithSession(func(session *sqlkit.Session) error {
			if _, err := session.Update(blogdb.Users).
				Set(blogdb.Users.Name.Set(ptr("Rolled back"))).
				Where(blogdb.Users.ID.Eq(aliceID)).
				Exec(ctx); err != nil {
				return err
			}
			// The write is visible inside the same transaction.
			inTxName = sessionUserName(t, ctx, session, aliceID)
			return rollback
		})
		if !errors.Is(err, rollback) {
			t.Fatalf("WithSession error = %v, want rollback", err)
		}
		if inTxName != "Rolled back" {
			t.Fatalf("in-tx name = %q, want Rolled back", inTxName)
		}
		if name := userName(t, db, aliceID); name != "Committed" {
			t.Fatalf("after rollback name = %q, want Committed intact", name)
		}

		// A manual session can insert several rows atomically and commit.
		session := db.NewSession()
		extra := insertUserTx(t, ctx, session, eng, userRow{Email: "dave@example.com"})
		if err := session.Commit(); err != nil {
			t.Fatalf("manual commit: %v", err)
		}
		count, err := db.Select().From(blogdb.Users).Where(blogdb.Users.ID.Eq(extra)).Count(ctx)
		if err != nil || count != 1 {
			t.Fatalf("dave after commit = %d, %v; want 1", count, err)
		}
	})
}

func userName(t *testing.T, db *sqlkit.Database, id int64) string {
	t.Helper()
	var row userRow
	if err := db.Select().From(blogdb.Users).Where(blogdb.Users.ID.Eq(id)).One(context.Background()).Scan(&row); err != nil {
		t.Fatalf("reload user %d: %v", id, err)
	}
	if row.Name == nil {
		return ""
	}
	return *row.Name
}

func sessionUserName(t *testing.T, ctx context.Context, session *sqlkit.Session, id int64) string {
	t.Helper()
	var row userRow
	if err := session.Select().From(blogdb.Users).Where(blogdb.Users.ID.Eq(id)).One(ctx).Scan(&row); err != nil {
		t.Fatalf("reload user %d in tx: %v", id, err)
	}
	if row.Name == nil {
		return ""
	}
	return *row.Name
}

// insertUserTx inserts a user through a session and returns its id. A *Session
// is a sqlkit.Executor, so the same dbtest.InsertID helper seeds inside the
// transaction. ctx and eng are unused now that dbtest owns the dialect split.
func insertUserTx(t *testing.T, ctx context.Context, session *sqlkit.Session, eng engine, u userRow) int64 {
	t.Helper()
	return dbtest.InsertID(t, session, blogdb.Users, u)
}
