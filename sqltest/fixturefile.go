package sqltest

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
)

// LoadFixtures loads every .json and .csv file in dir into the table named after
// the file, seeding through ex with no sqlkit.Table binding — the file-based,
// language-agnostic counterpart of Fixture (which declares rows as Go structs).
// It is the fixtures style of Rails and go-testfixtures: a directory of
// per-table data files a test shares as its starting state.
//
//	sqltest.LoadFixtures(t, db, sqlkit.PostgreSQLDialect{}, "testdata/blog")
//	// testdata/blog/01_users.json, testdata/blog/02_posts.csv → tables users, posts
//
// Files load in lexical filename order, so a numeric prefix controls foreign-key
// order (parents before children); the prefix is stripped from the table name,
// so 01_users.json loads into users. ex is any Execer (a *sql.DB, or a *sql.Tx a
// case rolls back), and dialect selects quoting and placeholders as in InsertMap.
// Subdirectories and files with other extensions are ignored.
func LoadFixtures(t *testing.T, ex Execer, dialect sqlkit.Dialect, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("sqltest: read fixtures dir %s: %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if ext := strings.ToLower(filepath.Ext(e.Name())); ext == ".json" || ext == ".csv" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		LoadFixtureFile(t, ex, dialect, tableNameFromFile(name), filepath.Join(dir, name))
	}
}

// LoadFixtureFile loads a single .json or .csv file into table through ex, for
// when the table name is decoupled from the filename or only one file is needed.
// The format is chosen by the file's extension. An empty file (or empty JSON
// array) inserts nothing.
func LoadFixtureFile(t *testing.T, ex Execer, dialect sqlkit.Dialect, table, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("sqltest: read fixture %s: %v", path, err)
	}
	var rows []map[string]any
	switch ext := strings.ToLower(filepath.Ext(path)); ext {
	case ".json":
		rows, err = decodeJSONRows(data)
	case ".csv":
		rows, err = decodeCSVRows(data)
	default:
		t.Fatalf("sqltest: fixture %s: unsupported extension %q (want .json or .csv)", path, ext)
	}
	if err != nil {
		t.Fatalf("sqltest: parse fixture %s: %v", path, err)
	}
	// Insert row by row so each keeps its own column set: a row that omits a
	// column gets that column's database default rather than a forced NULL.
	for i, row := range rows {
		if len(row) == 0 {
			t.Fatalf("sqltest: fixture %s row %d has no columns", path, i)
		}
		InsertMap(t, ex, dialect, table, row)
	}
}

// orderPrefixRE matches a leading numeric ordering prefix (e.g. "01_", "2-")
// that controls load order without becoming part of the table name.
var orderPrefixRE = regexp.MustCompile(`^\d+[-_]`)

// tableNameFromFile derives the table name from a fixture filename: the base
// name without its extension and without a leading numeric ordering prefix.
func tableNameFromFile(name string) string {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	return orderPrefixRE.ReplaceAllString(base, "")
}

// decodeJSONRows decodes a JSON array of objects into row maps. Numbers decode
// to int64 when whole and float64 otherwise (rather than always float64), and a
// nested array/object value re-encodes to its JSON text so it can seed a JSON
// column.
func decodeJSONRows(data []byte) ([]map[string]any, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	var raw []map[string]any
	if err := dec.Decode(&raw); err != nil {
		return nil, err
	}
	for _, row := range raw {
		for k, v := range row {
			row[k] = normalizeJSONValue(v)
		}
	}
	return raw, nil
}

// normalizeJSONValue maps a decoded JSON value to a database/sql-friendly form.
func normalizeJSONValue(v any) any {
	switch n := v.(type) {
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return i
		}
		if f, err := n.Float64(); err == nil {
			return f
		}
		return n.String()
	case []any, map[string]any:
		// A JSON array or object seeds a JSON column as its text form.
		if b, err := json.Marshal(n); err == nil {
			return string(b)
		}
		return v
	default:
		return v
	}
}

// decodeCSVRows decodes CSV with a header row of column names. Every value is a
// string except the unquoted token \N, which is NULL — the convention CSV cannot
// otherwise express. An empty file yields no rows.
func decodeCSVRows(data []byte) ([]map[string]any, error) {
	r := csv.NewReader(strings.NewReader(string(data)))
	records, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) < 1 {
		return nil, nil
	}
	header := records[0]
	rows := make([]map[string]any, 0, len(records)-1)
	for _, rec := range records[1:] {
		row := make(map[string]any, len(header))
		for i, col := range header {
			if rec[i] == `\N` {
				row[col] = nil
			} else {
				row[col] = rec[i]
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}
