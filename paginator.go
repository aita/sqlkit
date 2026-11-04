package sqlkit

import (
	"context"
	"database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/aita/sqlkit/internal/reflectmap"
	"github.com/aita/sqlkit/sql"
)

// Paginator performs keyset (cursor) pagination over a SelectQuery. Unlike
// LIMIT/OFFSET it walks the result by comparing against the last row already
// seen, so a page costs the same no matter how deep it is and rows inserted or
// deleted between requests do not shift the window.
//
// Configure it once with the sort keys and a page size, then call Paginate with
// a Cursor. The first page uses Start; each returned Page yields the Cursor for
// the following (Next) and preceding (Prev) pages, which you pass straight back:
//
//	pager := sqlkit.NewPaginator(
//		sqlkit.KeyDesc(Users.CreatedAt),
//		sqlkit.KeyAsc(Users.ID), // a unique tie-breaker, so the order is total
//	).Limit(20)
//
//	var users []User
//	page, err := pager.Paginate(ctx, db.Select().From(Users), &users, sqlkit.Start())
//	if page.HasNext {
//		page, err = pager.Paginate(ctx, db.Select().From(Users), &users, page.Next())
//	}
//
// End the keys with a unique column (typically the primary key) so the order is
// total and no row is skipped or repeated at a page boundary.
//
// A key whose Go type can hold NULL (a pointer, or a sql.Null*-style struct with
// a Valid field) is paginated NULL-safely: NULL sorts as the greatest value
// (last for KeyAsc, first for KeyDesc, on every dialect) and the seek predicate
// steps over NULLs explicitly, so rows with a NULL key are neither dropped nor
// repeated. Keys of non-nullable types keep the plain ORDER BY / comparison, so
// an index on them can serve the query directly.
type Paginator struct {
	keys  []PageKey
	limit int
	codec CursorCodec
}

// NewPaginator builds a Paginator that sorts by keys, in order. Provide at least
// one key, the last of which should be unique across rows. Set the page size
// with Limit before paginating.
func NewPaginator(keys ...PageKey) *Paginator {
	return &Paginator{keys: keys, codec: base64Codec{}}
}

// Limit sets the page size and returns the Paginator for chaining.
func (pg *Paginator) Limit(n int) *Paginator {
	pg.limit = n
	return pg
}

// WithCodec replaces how a cursor's bytes are turned into the opaque token
// string (and back), for signing, encryption, or a non-base64 form. The default
// is URL-safe base64. The codec only wraps the final envelope; the typed
// (de)serialization of the key values stays inside the Paginator, so cursor
// values keep their Go types regardless of the codec.
func (pg *Paginator) WithCodec(codec CursorCodec) *Paginator {
	pg.codec = codec
	return pg
}

// PageKey is one sort key of a Paginator: a column and its direction. It also
// remembers the column's Go type so the cursor's value for it decodes back to
// the right type (an int64 key stays an int64 with no float widening, a
// time.Time stays a time.Time). Build keys with KeyAsc and KeyDesc.
type PageKey struct {
	col      AnyColumn
	desc     bool
	nullable bool
	decode   func([]byte) (any, error)
}

// KeyAsc builds an ascending sort key for col. If col's Go type can hold NULL,
// NULLs sort after every non-NULL value.
func KeyAsc[T any](col Column[T]) PageKey {
	return PageKey{col: col, desc: false, nullable: nullableKeyType(reflect.TypeFor[T]()), decode: jsonDecoder[T]()}
}

// KeyDesc builds a descending sort key for col. If col's Go type can hold NULL,
// NULLs sort before every non-NULL value (NULL is the greatest value, as for
// KeyAsc).
func KeyDesc[T any](col Column[T]) PageKey {
	return PageKey{col: col, desc: true, nullable: nullableKeyType(reflect.TypeFor[T]()), decode: jsonDecoder[T]()}
}

// nullableKeyType reports whether a key column's Go type can represent SQL
// NULL: a pointer, interface, map, or slice (nil), or a sql.Null*-style struct
// carrying a bool Valid field (sql.NullString, sql.Null[T], and the like).
func nullableKeyType(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice:
		return true
	case reflect.Struct:
		f, ok := t.FieldByName("Valid")
		return ok && f.Type.Kind() == reflect.Bool
	}
	return false
}

func jsonDecoder[T any]() func([]byte) (any, error) {
	return func(b []byte) (any, error) {
		var v T
		if err := json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		return v, nil
	}
}

// CursorCodec turns a cursor's raw bytes into the opaque token string and back.
// Implement it to sign (HMAC), encrypt, or use a non-base64 format; the default
// is URL-safe base64.
type CursorCodec interface {
	Encode(raw []byte) (string, error)
	Decode(token string) ([]byte, error)
}

type base64Codec struct{}

func (base64Codec) Encode(raw []byte) (string, error) {
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (base64Codec) Decode(token string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(token)
}

// Cursor is an opaque position in a paginated result. It is produced by the
// Paginator — Start for the first page, and Page.Next / Page.Prev for the
// neighbours of a fetched page — never built by hand. Its direction is baked in,
// so it can only move the way it was meant to.
//
// To cross an HTTP request, render it with String and rebuild it with
// ParseCursor; treat the string as an opaque token, not a value to parse.
type Cursor struct {
	token string
	start bool
}

// Start is the cursor for the first page.
func Start() Cursor { return Cursor{start: true} }

// ParseCursor rebuilds a Cursor from the token String produced earlier, e.g. one
// received as an HTTP query parameter. An empty token yields the first-page
// cursor. The token is validated when it is used (Paginate), matching how the
// other builders defer errors to their terminal.
func ParseCursor(token string) Cursor {
	if token == "" {
		return Cursor{start: true}
	}
	return Cursor{token: token}
}

// String returns the opaque token to transport (URL, JSON). It is empty for the
// first-page cursor and for the absent neighbour of an edge page.
func (c Cursor) String() string { return c.token }

func (c Cursor) isStart() bool { return c.start || c.token == "" }

// Page is the metadata of a fetched page. The rows themselves were scanned into
// the destination passed to Paginate; Page reports whether neighbours exist and
// hands out the cursors to reach them.
type Page struct {
	HasNext bool
	HasPrev bool
	next    Cursor
	prev    Cursor
}

// Next returns the cursor for the following page (pass it back to Paginate). It
// is meaningful only when HasNext is true.
func (p Page) Next() Cursor { return p.next }

// Prev returns the cursor for the preceding page. It is meaningful only when
// HasPrev is true.
func (p Page) Prev() Cursor { return p.prev }

// Paginate fetches one page of q at cur and scans it into dst, a pointer to a
// slice of structs (or struct pointers). It builds the ORDER BY from the
// Paginator's keys, adds the seek predicate carried by cur, and fetches one row
// past the page size to detect whether more rows follow. The returned Page
// reports the neighbours and hands out their cursors.
//
// The cursor encodes a row's values for the key columns, so q must project them
// (the default SELECT of every column does). Do not also set q's OrderBy:
// Paginate orders by the Paginator's keys.
func (pg *Paginator) Paginate(ctx context.Context, q SelectQuery, dst any, cur Cursor) (Page, error) {
	if q.err != nil {
		return Page{}, q.err
	}
	if len(pg.keys) == 0 {
		return Page{}, fmt.Errorf("sqlkit: Paginator requires at least one key")
	}
	if pg.limit <= 0 {
		return Page{}, fmt.Errorf("sqlkit: Paginator.Limit must be set to a positive page size")
	}

	dv := reflect.ValueOf(dst)
	if dv.Kind() != reflect.Pointer || dv.IsNil() || dv.Elem().Kind() != reflect.Slice {
		return Page{}, fmt.Errorf("sqlkit: Paginate destination must be a non-nil pointer to a slice, got %T", dst)
	}

	start := cur.isStart()
	backward := false
	if !start {
		values, dir, err := pg.decodeToken(cur.token)
		if err != nil {
			return Page{}, err
		}
		backward = dir
		seek := make([]seekKey, len(pg.keys))
		for i, k := range pg.keys {
			seek[i] = seekKey{col: k.col, value: sql.Val(values[i]), desc: k.desc,
				nullable: k.nullable, isNull: k.nullable && isNullKeyValue(values[i])}
		}
		q = q.Where(seekPredicate(seek, backward))
	}
	q = q.OrderBy(pg.orderTerms(backward)...).Limit(pg.limit + 1)

	// Scan into a fresh slice of dst's element type, then trim the probe row and
	// (for a backward page) reverse to display order before handing it back.
	sliceType := dv.Elem().Type()
	tmp := reflect.New(sliceType)
	if err := q.All(ctx).Scan(tmp.Interface()); err != nil {
		return Page{}, err
	}
	result := tmp.Elem()
	extra := result.Len() > pg.limit
	if extra {
		result = result.Slice(0, pg.limit)
	}
	if backward {
		reverseSlice(result)
	}

	page := Page{}
	if backward {
		// Scanning backward: the probe row signals an earlier page, and we only
		// got here from a later one, so a next page exists — as long as this page
		// has rows. An empty backward page means the cursor was already at the
		// start (Prev past the first page); report no neighbours rather than a
		// HasNext with a zero, Start-equivalent cursor that would loop to page one.
		page.HasPrev = extra
		page.HasNext = result.Len() > 0
	} else {
		page.HasNext = extra
		// As in the backward branch above: an empty forward page (the cursor
		// landed past the last row) must not report a HasPrev that only leads
		// back to a zero, Start-equivalent cursor looping to page one.
		page.HasPrev = !start && result.Len() > 0
	}
	if result.Len() > 0 {
		// Next continues forward from the last row; Prev backward from the first.
		next, err := pg.makeCursor(result.Index(result.Len()-1), false)
		if err != nil {
			return Page{}, err
		}
		prev, err := pg.makeCursor(result.Index(0), true)
		if err != nil {
			return Page{}, err
		}
		page.next, page.prev = next, prev
	}
	dv.Elem().Set(result)
	return page, nil
}

// orderTerms builds the ORDER BY terms for the keys, flipping every direction
// when reverse is set (a backward page scans against the ordering). A nullable
// key is preceded by a "col IS NULL" term in the same direction, which places
// NULLs as the greatest value portably (MySQL has no NULLS FIRST/LAST, and the
// dialects disagree on where NULLs sort by default).
func (pg *Paginator) orderTerms(reverse bool) []sql.OrderTerm {
	terms := make([]sql.OrderTerm, 0, len(pg.keys))
	for _, k := range pg.keys {
		desc := k.desc
		if reverse {
			desc = !desc
		}
		order := sql.Asc
		if desc {
			order = sql.Desc
		}
		if k.nullable {
			terms = append(terms, order(sql.IsNull(k.col)))
		}
		terms = append(terms, order(k.col))
	}
	return terms
}

// makeCursor builds the cursor that continues from one scanned row: forward
// (backward=false) past it, or backward before it.
func (pg *Paginator) makeCursor(elem reflect.Value, backward bool) (Cursor, error) {
	values, err := pg.rowValues(elem)
	if err != nil {
		return Cursor{}, err
	}
	token, err := pg.encodeToken(values, backward)
	if err != nil {
		return Cursor{}, err
	}
	return Cursor{token: token}, nil
}

// rowValues reads the key columns' values from one scanned row, matching its
// fields to the key columns with the same name mapping as Scan.
func (pg *Paginator) rowValues(elem reflect.Value) ([]any, error) {
	for elem.Kind() == reflect.Pointer {
		if elem.IsNil() {
			return nil, fmt.Errorf("sqlkit: Paginate cannot build a cursor from a nil row")
		}
		elem = elem.Elem()
	}
	if elem.Kind() != reflect.Struct {
		return nil, fmt.Errorf("sqlkit: Paginate rows must be structs to build a cursor, got %s", elem.Kind())
	}
	fields := reflectmap.FieldsByColumn(elem.Type())
	values := make([]any, len(pg.keys))
	for i, k := range pg.keys {
		path, ok := fields[strings.ToLower(k.col.ColumnName())]
		if !ok {
			return nil, fmt.Errorf("sqlkit: Paginate row has no field for key column %q", k.col.ColumnName())
		}
		values[i] = elem.FieldByIndex(path).Interface()
	}
	return values, nil
}

// cursorPayload is the wire shape inside a token: the boundary key values and
// the direction the cursor moves (R=true means backward/Prev).
type cursorPayload struct {
	R bool  `json:"r"`
	V []any `json:"v"`
}

// encodeToken serializes the boundary values (with their Go types) and the
// direction, then wraps the bytes with the codec into an opaque token.
func (pg *Paginator) encodeToken(values []any, backward bool) (string, error) {
	raw, err := json.Marshal(cursorPayload{R: backward, V: values})
	if err != nil {
		return "", fmt.Errorf("sqlkit: Paginate encoding cursor: %w", err)
	}
	token, err := pg.codec.Encode(raw)
	if err != nil {
		return "", fmt.Errorf("sqlkit: Paginate encoding cursor: %w", err)
	}
	return token, nil
}

// decodeToken reverses encodeToken: it unwraps the codec, then decodes each
// boundary value through its key's Go type so the comparison binds the right
// type. It returns the values and whether the cursor moves backward.
func (pg *Paginator) decodeToken(token string) ([]any, bool, error) {
	raw, err := pg.codec.Decode(token)
	if err != nil {
		return nil, false, fmt.Errorf("sqlkit: Paginate decoding cursor: %w", err)
	}
	var payload struct {
		R bool              `json:"r"`
		V []json.RawMessage `json:"v"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, false, fmt.Errorf("sqlkit: Paginate decoding cursor: %w", err)
	}
	if len(payload.V) != len(pg.keys) {
		return nil, false, fmt.Errorf("sqlkit: Paginate cursor has %d values, want %d keys", len(payload.V), len(pg.keys))
	}
	values := make([]any, len(pg.keys))
	for i, k := range pg.keys {
		v, err := k.decode(payload.V[i])
		if err != nil {
			return nil, false, fmt.Errorf("sqlkit: Paginate decoding cursor value for %q: %w", k.col.ColumnName(), err)
		}
		values[i] = v
	}
	return values, payload.R, nil
}

// seekKey is one ordered key in the internal keyset comparison: a column, its
// direction, and the boundary value from the cursor.
type seekKey struct {
	col   sql.Expression
	value sql.Expression
	desc  bool
	// nullable marks a key whose column can hold NULL, compared NULL-safely
	// with NULL as the greatest value; isNull reports that the cursor's
	// boundary value for it is NULL.
	nullable bool
	isNull   bool
}

// seekPredicate builds the keyset predicate selecting the rows strictly after
// (before, when before is set) the cursor described by keys, under an ORDER BY
// matching the keys' columns and directions. It expands to the lexicographic
// comparison
//
//	(k0 > v0)
//	OR (k0 = v0 AND k1 > v1)
//	OR (k0 = v0 AND k1 = v1 AND k2 > v2)
//	...
//
// with each ">" flipped to "<" for a descending key (and all of them flipped
// again for a backward page). The OR form, rather than a row-value comparison
// (k0, k1) > (v0, v1), is used because it is portable across PostgreSQL and
// MySQL and handles mixed key directions.
//
// A nullable key compares NULL-safely, with NULL as the greatest value: its
// equality is "k IS NULL" at a NULL boundary, "greater than NULL" matches
// nothing (that OR term is dropped), "greater than v" also admits NULLs, and
// "less than NULL" is "k IS NOT NULL".
func seekPredicate(keys []seekKey, before bool) Predicate {
	ors := make([]sql.Expression, 0, len(keys))
	for i := range keys {
		cmp, ok := seekCompare(keys[i], before)
		if !ok {
			continue // nothing sorts past a NULL boundary in this direction
		}
		ands := make([]sql.Expression, 0, i+1)
		for j := 0; j < i; j++ {
			ands = append(ands, seekEqual(keys[j]))
		}
		ands = append(ands, cmp)
		ors = append(ors, sql.And(ands...))
	}
	if len(ors) == 0 {
		// Every key is at a NULL boundary scanning toward greater values: no row
		// lies strictly beyond the cursor.
		return newPredicate(sql.Raw("1 = 0"))
	}
	return newPredicate(sql.Or(ors...))
}

// seekEqual renders the equality of one key with its boundary value, NULL-safe
// for a nullable key.
func seekEqual(k seekKey) sql.Expression {
	if k.isNull {
		return sql.IsNull(k.col)
	}
	return sql.Eq(k.col, k.value)
}

// seekCompare renders the strict comparison for one key: "greater" when
// scanning in the key's own direction (ascending forward, descending backward),
// "less" otherwise. For a nullable key NULL is the greatest value, so it
// reports false when no value can be greater than a NULL boundary.
func seekCompare(k seekKey, before bool) (sql.Expression, bool) {
	greater := !k.desc != before
	switch {
	case !k.nullable && greater:
		return sql.Gt(k.col, k.value), true
	case !k.nullable:
		return sql.Lt(k.col, k.value), true
	case greater && k.isNull:
		return nil, false
	case greater:
		return sql.Or(sql.Gt(k.col, k.value), sql.IsNull(k.col)), true
	case k.isNull:
		return sql.IsNotNull(k.col), true
	default:
		return sql.Lt(k.col, k.value), true
	}
}

// isNullKeyValue reports whether a decoded cursor value stands for SQL NULL: a
// nil, a nil pointer/map/slice/interface, or a driver.Valuer (sql.NullString,
// ...) whose Value is nil.
func isNullKeyValue(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Interface:
		if rv.IsNil() {
			return true
		}
	}
	if valuer, ok := v.(driver.Valuer); ok {
		if dv, err := valuer.Value(); err == nil && dv == nil {
			return true
		}
	}
	return false
}

// reverseSlice reverses the elements of s (a reflect slice value) in place.
func reverseSlice(s reflect.Value) {
	swap := reflect.Swapper(s.Interface())
	for i, j := 0, s.Len()-1; i < j; i, j = i+1, j-1 {
		swap(i, j)
	}
}
