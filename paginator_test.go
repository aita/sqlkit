package sqlkit_test

import (
	"context"
	"database/sql/driver"
	"encoding/base64"
	"reflect"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
)

// paginatorFake returns a fake that serves the given rows and records the last
// statement and args.
func paginatorFake(rows [][]driver.Value, sqlOut *string, argsOut *[]any) *testutil.Fake {
	return &testutil.Fake{
		QueryFunc: func(sqlText string, args []any) (testutil.Result, error) {
			if sqlOut != nil {
				*sqlOut = sqlText
			}
			if argsOut != nil {
				*argsOut = args
			}
			return testutil.Result{Columns: []string{"id", "email", "name"}, Rows: rows}, nil
		},
	}
}

func threeUsers() [][]driver.Value {
	return [][]driver.Value{
		{int64(1), "a@example.com", "Alice"},
		{int64(2), "b@example.com", "Bob"},
		{int64(3), "c@example.com", "Carol"},
	}
}

func TestPaginateFirstPage(t *testing.T) {
	var gotSQL string
	db := sqlkit.WrapDB(paginatorFake(threeUsers(), &gotSQL, nil).DB())
	pager := sqlkit.NewPaginator(sqlkit.KeyAsc(Users.Email), sqlkit.KeyAsc(Users.ID)).Limit(2)

	var users []MyUserDTO
	page, err := pager.Paginate(context.Background(), db.Select().From(Users), &users, sqlkit.Start())
	if err != nil {
		t.Fatalf("Paginate() error = %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("rows = %d, want 2 (probe row trimmed)", len(users))
	}
	if users[0].Identifier != 1 || users[1].Identifier != 2 {
		t.Fatalf("rows = %#v", users)
	}
	if !page.HasNext {
		t.Fatal("HasNext = false, want true (a third row was available)")
	}
	if page.HasPrev {
		t.Fatal("HasPrev = true, want false on the first page")
	}
	if strings.Contains(gotSQL, "WHERE") {
		t.Fatalf("first page should carry no seek WHERE, sql = %q", gotSQL)
	}
	if !strings.Contains(gotSQL, `ORDER BY "public"."users"."email", "public"."users"."id"`) {
		t.Fatalf("sql = %q, want ORDER BY on the keys", gotSQL)
	}
	if !strings.Contains(gotSQL, "LIMIT 3") {
		t.Fatalf("sql = %q, want LIMIT 3 (limit+1)", gotSQL)
	}
	if page.Next().String() == "" {
		t.Fatal("Next() cursor is empty, want a token")
	}
}

func TestPaginateLastPageHasNoNext(t *testing.T) {
	rows := [][]driver.Value{
		{int64(1), "a@example.com", "Alice"},
		{int64(2), "b@example.com", "Bob"},
	}
	db := sqlkit.WrapDB(paginatorFake(rows, nil, nil).DB())
	pager := sqlkit.NewPaginator(sqlkit.KeyAsc(Users.ID)).Limit(2)

	var users []MyUserDTO
	page, err := pager.Paginate(context.Background(), db.Select().From(Users), &users, sqlkit.Start())
	if err != nil {
		t.Fatalf("Paginate() error = %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("rows = %d, want 2", len(users))
	}
	if page.HasNext {
		t.Fatal("HasNext = true, want false on the last page")
	}
}

func TestPaginateNextAppliesSeek(t *testing.T) {
	var lastSQL string
	var lastArgs []any
	db := sqlkit.WrapDB(paginatorFake(threeUsers(), &lastSQL, &lastArgs).DB())
	pager := sqlkit.NewPaginator(sqlkit.KeyAsc(Users.Email), sqlkit.KeyAsc(Users.ID)).Limit(2)

	var p1 []MyUserDTO
	page1, err := pager.Paginate(context.Background(), db.Select().From(Users), &p1, sqlkit.Start())
	if err != nil {
		t.Fatalf("first page error = %v", err)
	}

	var p2 []MyUserDTO
	page2, err := pager.Paginate(context.Background(), db.Select().From(Users), &p2, page1.Next())
	if err != nil {
		t.Fatalf("next page error = %v", err)
	}
	if !page2.HasPrev {
		t.Fatal("HasPrev = false, want true after navigating forward")
	}
	wantWhere := `WHERE (("public"."users"."email" > $1) OR (("public"."users"."email" = $2) AND ("public"."users"."id" > $3)))`
	if !strings.Contains(lastSQL, wantWhere) {
		t.Fatalf("next page sql = %q,\nwant it to contain %q", lastSQL, wantWhere)
	}
	// The cursor's values round-trip through the key types: the boundary email
	// (compared twice) and the boundary id as a real int64, not a float.
	wantArgs := []any{"b@example.com", "b@example.com", int64(2)}
	if !reflect.DeepEqual(lastArgs, wantArgs) {
		t.Fatalf("seek args = %#v, want %#v", lastArgs, wantArgs)
	}
}

func TestPaginateDescendingKeySeeksLessThan(t *testing.T) {
	var lastSQL string
	rows := [][]driver.Value{
		{int64(3), "c@example.com", "Carol"},
		{int64(2), "b@example.com", "Bob"},
	}
	db := sqlkit.WrapDB(paginatorFake(rows, &lastSQL, nil).DB())
	pager := sqlkit.NewPaginator(sqlkit.KeyDesc(Users.ID)).Limit(1)

	var p1 []MyUserDTO
	page1, err := pager.Paginate(context.Background(), db.Select().From(Users), &p1, sqlkit.Start())
	if err != nil {
		t.Fatalf("first page error = %v", err)
	}
	var p2 []MyUserDTO
	if _, err := pager.Paginate(context.Background(), db.Select().From(Users), &p2, page1.Next()); err != nil {
		t.Fatalf("next page error = %v", err)
	}
	if !strings.Contains(lastSQL, `WHERE ("public"."users"."id" < $1)`) {
		t.Fatalf("descending forward seek should use '<', sql = %q", lastSQL)
	}
	if !strings.Contains(lastSQL, `ORDER BY "public"."users"."id" DESC`) {
		t.Fatalf("sql = %q, want ORDER BY id DESC", lastSQL)
	}
}

func TestPaginateCursorPreservesLargeInt64(t *testing.T) {
	var lastArgs []any
	bigID := int64(9007199254740993) // 2^53+1, not representable as float64
	rows := [][]driver.Value{
		{bigID, "a@example.com", "Alice"},
		{bigID + 1, "b@example.com", "Bob"},
	}
	db := sqlkit.WrapDB(paginatorFake(rows, nil, &lastArgs).DB())
	pager := sqlkit.NewPaginator(sqlkit.KeyAsc(Users.ID)).Limit(1)

	var p1 []MyUserDTO
	page1, err := pager.Paginate(context.Background(), db.Select().From(Users), &p1, sqlkit.Start())
	if err != nil {
		t.Fatalf("first page error = %v", err)
	}
	var p2 []MyUserDTO
	if _, err := pager.Paginate(context.Background(), db.Select().From(Users), &p2, page1.Next()); err != nil {
		t.Fatalf("next page error = %v", err)
	}
	if len(lastArgs) != 1 || lastArgs[0] != bigID {
		t.Fatalf("seek arg = %#v, want exact int64 %d", lastArgs, bigID)
	}
}

func TestPaginatePrevReversesResult(t *testing.T) {
	var lastSQL string
	db := sqlkit.WrapDB(paginatorFake(threeUsers(), &lastSQL, nil).DB())
	pager := sqlkit.NewPaginator(sqlkit.KeyAsc(Users.ID)).Limit(2)

	// Obtain a valid cursor to page backward from.
	var p1 []MyUserDTO
	page1, err := pager.Paginate(context.Background(), db.Select().From(Users), &p1, sqlkit.Start())
	if err != nil {
		t.Fatalf("first page error = %v", err)
	}

	var prev []MyUserDTO
	page, err := pager.Paginate(context.Background(), db.Select().From(Users), &prev, page1.Prev())
	if err != nil {
		t.Fatalf("prev page error = %v", err)
	}
	// The query scans against the ordering (DESC) and Paginate reverses the rows
	// back to display order: the fake's [1,2,3] trims to [1,2] then reverses.
	if len(prev) != 2 || prev[0].Identifier != 2 || prev[1].Identifier != 1 {
		t.Fatalf("prev = %#v, want ids [2 1] in display order", prev)
	}
	if !page.HasNext {
		t.Fatal("HasNext = false, want true after paging backward")
	}
	if !page.HasPrev {
		t.Fatal("HasPrev = false, want true (a probe row was available)")
	}
	if !strings.Contains(lastSQL, `ORDER BY "public"."users"."id" DESC`) {
		t.Fatalf("backward page should scan DESC, sql = %q", lastSQL)
	}
	if !strings.Contains(lastSQL, `WHERE ("public"."users"."id" < $1)`) {
		t.Fatalf("backward page should seek with '<', sql = %q", lastSQL)
	}
}

// TestPaginateEmptyBackwardReportsNoNext covers paging backward past the first
// page (an empty backward result). HasNext must be false: there is no row to
// build a forward cursor from, so a true HasNext would hand back a zero,
// Start-equivalent cursor that loops to page one.
func TestPaginateEmptyBackwardReportsNoNext(t *testing.T) {
	calls := 0
	fake := &testutil.Fake{
		QueryFunc: func(string, []any) (testutil.Result, error) {
			calls++
			cols := []string{"id", "email", "name"}
			if calls == 1 {
				return testutil.Result{Columns: cols, Rows: threeUsers()}, nil
			}
			return testutil.Result{Columns: cols}, nil // backward page: no earlier rows
		},
	}
	db := sqlkit.WrapDB(fake.DB())
	pager := sqlkit.NewPaginator(sqlkit.KeyAsc(Users.ID)).Limit(2)

	var p1 []MyUserDTO
	page1, err := pager.Paginate(context.Background(), db.Select().From(Users), &p1, sqlkit.Start())
	if err != nil {
		t.Fatalf("first page error = %v", err)
	}

	var prev []MyUserDTO
	page, err := pager.Paginate(context.Background(), db.Select().From(Users), &prev, page1.Prev())
	if err != nil {
		t.Fatalf("prev page error = %v", err)
	}
	if len(prev) != 0 {
		t.Fatalf("prev = %#v, want empty", prev)
	}
	if page.HasNext {
		t.Fatal("HasNext = true on an empty backward page; want false")
	}
	if page.HasPrev {
		t.Fatal("HasPrev = true on an empty backward page; want false")
	}
}

func TestPaginateAcrossHTTPRoundTrip(t *testing.T) {
	var lastArgs []any
	db := sqlkit.WrapDB(paginatorFake(threeUsers(), nil, &lastArgs).DB())
	pager := sqlkit.NewPaginator(sqlkit.KeyAsc(Users.ID)).Limit(2)

	var p1 []MyUserDTO
	page1, err := pager.Paginate(context.Background(), db.Select().From(Users), &p1, sqlkit.Start())
	if err != nil {
		t.Fatalf("first page error = %v", err)
	}

	// Simulate the cursor crossing an HTTP boundary: token out, token back in.
	token := page1.Next().String()
	if token == "" {
		t.Fatal("Next() token is empty")
	}
	cur := sqlkit.ParseCursor(token)

	var p2 []MyUserDTO
	if _, err := pager.Paginate(context.Background(), db.Select().From(Users), &p2, cur); err != nil {
		t.Fatalf("paginate from parsed cursor error = %v", err)
	}
	if len(lastArgs) != 1 || lastArgs[0] != int64(2) {
		t.Fatalf("seek arg after round-trip = %#v, want int64(2)", lastArgs)
	}
}

func TestParseEmptyCursorIsStart(t *testing.T) {
	if sqlkit.ParseCursor("").String() != sqlkit.Start().String() {
		t.Fatal("ParseCursor(\"\") should equal Start()")
	}
	db := sqlkit.WrapDB(paginatorFake(threeUsers(), nil, nil).DB())
	pager := sqlkit.NewPaginator(sqlkit.KeyAsc(Users.ID)).Limit(2)
	var users []MyUserDTO
	page, err := pager.Paginate(context.Background(), db.Select().From(Users), &users, sqlkit.ParseCursor(""))
	if err != nil {
		t.Fatalf("Paginate(ParseCursor(\"\")) error = %v", err)
	}
	if page.HasPrev {
		t.Fatal("empty cursor should behave as the first page (HasPrev=false)")
	}
}

// prefixCodec wraps the default base64 with a recognizable prefix, to prove
// WithCodec controls the envelope.
type prefixCodec struct{}

func (prefixCodec) Encode(raw []byte) (string, error) {
	return "px_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

func (prefixCodec) Decode(token string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, "px_"))
}

func TestPaginateWithCustomCodec(t *testing.T) {
	var lastArgs []any
	db := sqlkit.WrapDB(paginatorFake(threeUsers(), nil, &lastArgs).DB())
	pager := sqlkit.NewPaginator(sqlkit.KeyAsc(Users.ID)).Limit(2).WithCodec(prefixCodec{})

	var p1 []MyUserDTO
	page1, err := pager.Paginate(context.Background(), db.Select().From(Users), &p1, sqlkit.Start())
	if err != nil {
		t.Fatalf("first page error = %v", err)
	}
	if !strings.HasPrefix(page1.Next().String(), "px_") {
		t.Fatalf("custom codec not applied to token: %q", page1.Next().String())
	}
	var p2 []MyUserDTO
	if _, err := pager.Paginate(context.Background(), db.Select().From(Users), &p2, page1.Next()); err != nil {
		t.Fatalf("next page error = %v", err)
	}
	if len(lastArgs) != 1 || lastArgs[0] != int64(2) {
		t.Fatalf("seek arg = %#v, want int64(2)", lastArgs)
	}
}

func TestPaginateValidation(t *testing.T) {
	db := sqlkit.WrapDB(usersFake().DB())
	ctx := context.Background()

	cases := []struct {
		name  string
		pager *sqlkit.Paginator
	}{
		{"no keys", sqlkit.NewPaginator().Limit(10)},
		{"limit not set", sqlkit.NewPaginator(sqlkit.KeyAsc(Users.ID))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var users []MyUserDTO
			if _, err := tc.pager.Paginate(ctx, db.Select().From(Users), &users, sqlkit.Start()); err == nil {
				t.Fatal("Paginate() error = nil, want an error")
			}
		})
	}
}

func TestPaginateRejectsBadCursor(t *testing.T) {
	db := sqlkit.WrapDB(usersFake().DB())
	pager := sqlkit.NewPaginator(sqlkit.KeyAsc(Users.ID)).Limit(5)
	var users []MyUserDTO
	_, err := pager.Paginate(context.Background(), db.Select().From(Users), &users, sqlkit.ParseCursor("not-a-valid-cursor!!"))
	if err == nil {
		t.Fatal("Paginate with a malformed cursor should error")
	}
}

// A NULL value in a key column cannot anchor a keyset seek — SQL's >/</= against
// NULL are never true, so the next page would silently match no rows and drop the
// rest of the result. Paginate must reject building a cursor at a NULL key value
// with a descriptive error rather than page into an empty set. (A non-NULL key
// column, the documented requirement, is unaffected — see the other tests.)
func TestPaginateNullKeyValueRejected(t *testing.T) {
	rows := [][]driver.Value{
		{int64(1), "a@example.com", nil}, // name is NULL
		{int64(2), "b@example.com", nil},
		{int64(3), "c@example.com", "Carol"},
	}
	db := sqlkit.WrapDB(paginatorFake(rows, nil, nil).DB())
	pager := sqlkit.NewPaginator(sqlkit.KeyAsc(Users.Name), sqlkit.KeyAsc(Users.ID)).Limit(2)

	var users []MyUserDTO
	_, err := pager.Paginate(context.Background(), db.Select().From(Users), &users, sqlkit.Start())
	if err == nil {
		t.Fatal("Paginate() error = nil, want a NULL-key rejection (a NULL boundary silently drops rows)")
	}
	if !strings.Contains(err.Error(), "NULL") || !strings.Contains(err.Error(), "name") {
		t.Fatalf("Paginate() error = %v, want a descriptive NULL-key-column rejection", err)
	}
}
