package introspection

import (
	"context"
	stdsql "database/sql"
	"database/sql/driver"
	"reflect"
	"strings"
	"testing"

	"github.com/aita/sqlkit/internal/testutil"
	"github.com/aita/sqlkit/meta"
	"github.com/aita/sqlkit/sql"
)

func TestInspectRejectsUnsupportedDialects(t *testing.T) {
	if _, err := Schema(context.Background(), nil, "oracle", ""); err == nil {
		t.Fatal("Schema(oracle) succeeded, want unsupported error")
	}
}

// TestReferentialAction exercises every information_schema rule string mapping,
// including case/whitespace normalization and the passthrough default that
// preserves an unknown rule verbatim (uppercased).
func TestReferentialAction(t *testing.T) {
	tests := []struct {
		rule string
		want meta.ReferentialAction
	}{
		{"", ""},
		{"NO ACTION", ""},
		{"no action", ""},
		{"  NO ACTION  ", ""},
		{"RESTRICT", meta.Restrict},
		{"restrict", meta.Restrict},
		{"CASCADE", meta.Cascade},
		{"cascade", meta.Cascade},
		{"SET NULL", meta.SetNull},
		{"set null", meta.SetNull},
		{"SET DEFAULT", meta.SetDefault},
		{"set default", meta.SetDefault},
		// Unknown rule: preserved verbatim, uppercased and trimmed.
		{"  weird rule ", meta.ReferentialAction("WEIRD RULE")},
	}
	for _, tt := range tests {
		if got := referentialAction(tt.rule); got != tt.want {
			t.Errorf("referentialAction(%q) = %q, want %q", tt.rule, got, tt.want)
		}
	}
}

// TestNormalizeCheckClause covers stripping the CHECK keyword and balanced
// outer parentheses, while leaving unbalanced or non-enclosing parens intact.
func TestNormalizeCheckClause(t *testing.T) {
	tests := []struct {
		clause string
		want   string
	}{
		{"CHECK ((balance >= (0)::numeric))", "balance >= (0)::numeric"},
		{"(`balance` >= 0)", "`balance` >= 0"},
		{"CHECK (a > 0)", "a > 0"},
		{"a > 0", "a > 0"},
		{"   CHECK   (  x  )  ", "x"},
		// Not enclosed by a single outer pair: "(a > 0) AND (b > 0)" starts and
		// ends with parens, but stripping them leaves unbalanced content, so the
		// clause must be preserved unchanged.
		{"((a > 0) AND (b > 0))", "(a > 0) AND (b > 0)"},
		{"(a > 0) AND (b > 0)", "(a > 0) AND (b > 0)"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := normalizeCheckClause(tt.clause); got != tt.want {
			t.Errorf("normalizeCheckClause(%q) = %q, want %q", tt.clause, got, tt.want)
		}
	}
}

// TestBalancedParens covers the depth-goes-negative early return as well as
// the balanced and unbalanced terminal cases.
func TestBalancedParens(t *testing.T) {
	tests := []struct {
		s    string
		want bool
	}{
		{"", true},
		{"a > 0", true},
		{"(a) (b)", true},
		{")a(", false}, // depth dips below zero
		{"(a", false},  // ends positive
		{"a)", false},  // dips below zero immediately
	}
	for _, tt := range tests {
		if got := balancedParens(tt.s); got != tt.want {
			t.Errorf("balancedParens(%q) = %v, want %v", tt.s, got, tt.want)
		}
	}
}

// TestColumnDefault covers the serial/identity, NULL, nextval sequence, and
// raw-clause branches of default derivation.
func TestColumnDefault(t *testing.T) {
	tests := []struct {
		name       string
		def        stdsql.NullString
		serial     bool
		wantHas    bool
		wantExpr   sql.Expression
		wantNilExp bool
	}{
		{name: "serial drops expression", def: stdsql.NullString{String: "x", Valid: true}, serial: true, wantHas: true, wantNilExp: true},
		{name: "no default", def: stdsql.NullString{}, wantHas: false, wantNilExp: true},
		{name: "nextval sequence drops expression", def: stdsql.NullString{String: "nextval('s'::regclass)", Valid: true}, wantHas: true, wantNilExp: true},
		{name: "raw clause preserved", def: stdsql.NullString{String: "0", Valid: true}, wantHas: true, wantExpr: sql.Raw("0")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			has, expr := columnDefault(tt.def, tt.serial)
			if has != tt.wantHas {
				t.Errorf("HasDefault = %v, want %v", has, tt.wantHas)
			}
			if tt.wantNilExp {
				if expr != nil {
					t.Errorf("expr = %#v, want nil", expr)
				}
				return
			}
			if !reflect.DeepEqual(expr, tt.wantExpr) {
				t.Errorf("expr = %#v, want %#v", expr, tt.wantExpr)
			}
		})
	}
}

// TestColumnTypeNonDecimalDropsPrecision verifies the precision/scale clearing
// for non-decimal types and that decimal/numeric retain them, in both dialects.
func TestColumnTypeNonDecimalDropsPrecision(t *testing.T) {
	// information_schema reports a numeric_precision for integer types too;
	// columnType must drop it for non-decimal types.
	if got := columnType("postgres", "int8", 0, 64, 0); !reflect.DeepEqual(got, meta.Of("postgres", "int8", 0, 0, 0)) {
		t.Errorf("int8 type = %#v, want precision dropped", got)
	}
	if got := columnType("mysql", "bigint", 0, 19, 0); !reflect.DeepEqual(got, meta.Of("mysql", "bigint", 0, 0, 0)) {
		t.Errorf("bigint type = %#v, want precision dropped", got)
	}
	// decimal/numeric keep precision and scale.
	if got := columnType("postgres", "numeric", 0, 10, 2); !reflect.DeepEqual(got, meta.Of("postgres", "numeric", 0, 10, 2)) {
		t.Errorf("numeric type = %#v, want precision kept", got)
	}
	if got := columnType("mysql", "decimal", 0, 10, 2); !reflect.DeepEqual(got, meta.Of("mysql", "decimal", 0, 10, 2)) {
		t.Errorf("decimal type = %#v, want precision kept", got)
	}
	// MySQL underscore prefix is NOT treated as an array (array detection is
	// PostgreSQL-only).
	if got := columnType("mysql", "_weird", 0, 0, 0); !reflect.DeepEqual(got, meta.Of("mysql", "_weird", 0, 0, 0)) {
		t.Errorf("mysql _weird = %#v, want non-array literal type", got)
	}
}

// TestAttachForeignKeysGroupsCompositeAndSkipsUnknown verifies multi-column FK
// grouping under one constraint and that rows for unknown tables are skipped.
func TestAttachForeignKeysGroupsCompositeAndSkipsUnknown(t *testing.T) {
	tables := []meta.Table{{Name: "orders"}}
	rows := []fkRow{
		{constraint: "fk_a_b", table: "orders", column: "a", refSchema: "public", refTable: "ref", refColumn: "ra", onDelete: "CASCADE", onUpdate: "RESTRICT"},
		{constraint: "fk_a_b", table: "orders", column: "b", refSchema: "public", refTable: "ref", refColumn: "rb"},
		// Unknown table: silently skipped.
		{constraint: "fk_x", table: "missing", column: "x", refTable: "ref", refColumn: "rx"},
	}
	attachForeignKeys(tables, rows)
	if len(tables[0].ForeignKeys) != 1 {
		t.Fatalf("foreign keys = %d, want 1 grouped constraint", len(tables[0].ForeignKeys))
	}
	fk := tables[0].ForeignKeys[0]
	if !reflect.DeepEqual(fk.Columns, []string{"a", "b"}) || !reflect.DeepEqual(fk.RefColumns, []string{"ra", "rb"}) {
		t.Fatalf("composite columns = %#v / %#v", fk.Columns, fk.RefColumns)
	}
	if fk.OnDelete != meta.Cascade || fk.OnUpdate != meta.Restrict {
		t.Fatalf("actions = %q / %q", fk.OnDelete, fk.OnUpdate)
	}
}

// TestAttachIndexesGroupsAndSkipsUnknown verifies multi-column index grouping
// and that rows for unknown tables are skipped.
func TestAttachIndexesGroupsAndSkipsUnknown(t *testing.T) {
	tables := []meta.Table{{Name: "t"}}
	rows := []indexRow{
		{table: "t", index: "ix", unique: true, column: "a"},
		{table: "t", index: "ix", unique: true, column: "b"},
		{table: "gone", index: "ix2", column: "c"},
	}
	attachIndexes(tables, rows)
	if len(tables[0].Indexes) != 1 {
		t.Fatalf("indexes = %d, want 1", len(tables[0].Indexes))
	}
	if ix := tables[0].Indexes[0]; !ix.Unique || !reflect.DeepEqual(ix.Columns, []string{"a", "b"}) {
		t.Fatalf("index = %#v", ix)
	}
}

// TestAttachCheckUnknownTableIsNoOp verifies that a check for a table not in the
// slice is dropped rather than panicking.
func TestAttachCheckUnknownTableIsNoOp(t *testing.T) {
	tables := []meta.Table{{Name: "t"}}
	attachCheck(tables, "missing", "c", "CHECK (a > 0)")
	if len(tables[0].Checks) != 0 {
		t.Fatalf("checks = %d, want 0 (unknown table ignored)", len(tables[0].Checks))
	}
	attachCheck(tables, "t", "c", "CHECK ((a > 0))")
	if len(tables[0].Checks) != 1 || tables[0].Checks[0].Expression != sql.Raw("a > 0") {
		t.Fatalf("checks = %#v", tables[0].Checks)
	}
}

func TestInspectPostgres(t *testing.T) {
	fake := &testutil.Fake{
		QueryFunc: func(sql string, args []any) (testutil.Result, error) {
			switch {
			case strings.Contains(sql, "con.contype = 'f'"):
				return testutil.Result{
					Columns: []string{"constraint_name", "table_name", "column_name", "ref_schema", "ref_table", "ref_column", "delete_rule", "update_rule"},
					Rows: [][]driver.Value{
						{"posts_user_id_fkey", "posts", "user_id", "public", "users", "id", "CASCADE", "NO ACTION"},
					},
				}, nil
			case strings.Contains(sql, "PRIMARY KEY"):
				return testutil.Result{
					Columns: []string{"table_name", "column_name"},
					Rows: [][]driver.Value{
						{"users", "id"},
						{"posts", "id"},
					},
				}, nil
			case strings.Contains(sql, "pg_index"):
				return testutil.Result{
					Columns: []string{"table_name", "index_name", "unique", "column_name"},
					Rows: [][]driver.Value{
						{"users", "users_email_key", true, "email"},
						{"posts", "idx_posts_user_id", false, "user_id"},
					},
				}, nil
			case strings.Contains(sql, "pg_constraint"):
				return testutil.Result{
					Columns: []string{"table_name", "constraint_name", "clause"},
					Rows: [][]driver.Value{
						{"users", "users_balance_check", "CHECK ((balance >= (0)::numeric))"},
					},
				}, nil
			case strings.Contains(sql, "pg_description"):
				return testutil.Result{
					Columns: []string{"table_name", "column_name", "comment"},
					Rows: [][]driver.Value{
						{"users", "", "Registered accounts"},
						{"users", "email", "Unique login address"},
					},
				}, nil
			default:
				return testutil.Result{
					Columns: []string{"table_name", "column_name", "udt_name", "nullable", "column_default", "is_identity", "length", "precision", "scale"},
					Rows: [][]driver.Value{
						{"users", "id", "int8", false, "nextval('users_id_seq'::regclass)", false, 0, 64, 0},
						{"users", "email", "varchar", false, nil, false, 255, 0, 0},
						{"users", "balance", "numeric", false, "0", false, 0, 10, 2},
						{"posts", "id", "int8", false, "nextval('posts_id_seq'::regclass)", false, 0, 64, 0},
						{"posts", "user_id", "int8", false, nil, false, 0, 64, 0},
					},
				}, nil
			}
		},
	}

	got, err := Schema(context.Background(), fake.DB(), "postgres", "public")
	if err != nil {
		t.Fatalf("Schema() error = %v", err)
	}

	want := meta.Metadata{Tables: []meta.Table{
		{
			Schema:  meta.Schema{Name: "public"},
			Name:    "users",
			Comment: "Registered accounts",
			Columns: []meta.Column{
				{Name: "id", Type: meta.Of("postgres", "int8", 0, 0, 0), HasDefault: true, PrimaryKey: true},
				{Name: "email", Type: meta.Of("postgres", "varchar", 255, 0, 0), Comment: "Unique login address"},
				{Name: "balance", Type: meta.Of("postgres", "numeric", 0, 10, 2), HasDefault: true, DefaultExpr: sql.Raw("0")},
			},
			Indexes: []meta.Index{
				{Name: "users_email_key", Columns: []string{"email"}, Unique: true},
			},
			Checks: []meta.Check{
				{Name: "users_balance_check", Expression: sql.Raw("balance >= (0)::numeric")},
			},
		},
		{
			Schema: meta.Schema{Name: "public"},
			Name:   "posts",
			Columns: []meta.Column{
				{Name: "id", Type: meta.Of("postgres", "int8", 0, 0, 0), HasDefault: true, PrimaryKey: true},
				{Name: "user_id", Type: meta.Of("postgres", "int8", 0, 0, 0)},
			},
			ForeignKeys: []meta.ForeignKey{
				{Name: "posts_user_id_fkey", Columns: []string{"user_id"}, RefSchema: meta.Schema{Name: "public"}, RefTable: "users", RefColumns: []string{"id"}, OnDelete: meta.Cascade},
			},
			Indexes: []meta.Index{
				{Name: "idx_posts_user_id", Columns: []string{"user_id"}},
			},
		},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Schema() = %#v,\nwant %#v", got, want)
	}

	queries := fake.Queries()
	if len(queries) != 6 {
		t.Fatalf("queries = %d, want 6 (columns, primary keys, foreign keys, indexes, checks, comments)", len(queries))
	}
	for _, query := range queries {
		if !reflect.DeepEqual(query.Args, []any{"public"}) {
			t.Fatalf("query args = %#v, want schema bind", query.Args)
		}
	}
}

// TestInspectPostgresArray: PostgreSQL reports array columns with an
// underscore-prefixed udt_name (e.g. "_int4", "_text"); the introspection strips the
// prefix and resolves an array of the element type.
func TestInspectPostgresArray(t *testing.T) {
	fake := &testutil.Fake{
		QueryFunc: func(query string, args []any) (testutil.Result, error) {
			if strings.Contains(query, "information_schema.columns") {
				return testutil.Result{
					Columns: []string{"table_name", "column_name", "udt_name", "nullable", "column_default", "is_identity", "length", "precision", "scale"},
					Rows: [][]driver.Value{
						{"docs", "id", "int8", false, nil, false, 0, 64, 0},
						{"docs", "tags", "_text", false, nil, false, 0, 0, 0},
						{"docs", "scores", "_int4", true, nil, false, 0, 0, 0},
					},
				}, nil
			}
			return testutil.Result{}, nil
		},
	}

	got, err := Schema(context.Background(), fake.DB(), "postgres", "public")
	if err != nil {
		t.Fatalf("Schema() error = %v", err)
	}
	cols := got.Tables[0].Columns
	want := []meta.Column{
		{Name: "id", Type: meta.Of("postgres", "int8", 0, 0, 0)},
		{Name: "tags", Type: meta.OfArray("postgres", "text", 0, 0, 0)},
		{Name: "scores", Type: meta.OfArray("postgres", "int4", 0, 0, 0), Nullable: true},
	}
	if !reflect.DeepEqual(cols, want) {
		t.Fatalf("array columns = %#v,\nwant %#v", cols, want)
	}
	// The resolved engine carries the array flag, so DDL and codegen render slices.
	if eng, _ := cols[1].Type.Engine("postgres"); !eng.Array || eng.Name != "text" {
		t.Errorf("tags engine = %+v, want {Name:text, Array:true}", eng)
	}
}

func TestInspectMySQL(t *testing.T) {
	fake := &testutil.Fake{
		QueryFunc: func(sql string, args []any) (testutil.Result, error) {
			switch {
			case strings.Contains(sql, "REFERENCED_TABLE_NAME"):
				return testutil.Result{
					Columns: []string{"constraint_name", "table_name", "column_name", "ref_schema", "ref_table", "ref_column", "delete_rule", "update_rule"},
					Rows: [][]driver.Value{
						{"fk_posts_user", "posts", "user_id", "app", "users", "id", "CASCADE", "NO ACTION"},
					},
				}, nil
			case strings.Contains(sql, "STATISTICS"):
				return testutil.Result{
					Columns: []string{"table_name", "index_name", "unique", "column_name"},
					Rows: [][]driver.Value{
						{"users", "uq_users_email", true, "email"},
					},
				}, nil
			case strings.Contains(sql, "CHECK_CONSTRAINTS"):
				return testutil.Result{
					Columns: []string{"table_name", "constraint_name", "clause"},
					Rows: [][]driver.Value{
						{"users", "users_chk_1", "(`balance` >= 0)"},
					},
				}, nil
			default:
				return testutil.Result{
					Columns: []string{"table_name", "column_name", "data_type", "nullable", "column_default", "auto_increment", "is_pk", "length", "precision", "scale", "column_comment", "table_comment"},
					Rows: [][]driver.Value{
						{"users", "id", "bigint", false, nil, true, true, 0, 19, 0, "", "Registered accounts"},
						{"users", "email", "varchar", false, nil, false, false, 255, 0, 0, "Unique login address", "Registered accounts"},
						{"users", "balance", "decimal", false, "0.00", false, false, 0, 10, 2, "", "Registered accounts"},
						{"posts", "id", "bigint", false, nil, true, true, 0, 19, 0, "", ""},
						{"posts", "user_id", "bigint", false, nil, false, false, 0, 19, 0, "", ""},
					},
				}, nil
			}
		},
	}

	got, err := Schema(context.Background(), fake.DB(), "mysql", "app")
	if err != nil {
		t.Fatalf("Schema() error = %v", err)
	}

	want := meta.Metadata{Tables: []meta.Table{
		{
			Schema:  meta.Schema{Name: "app"},
			Name:    "users",
			Comment: "Registered accounts",
			Columns: []meta.Column{
				{Name: "id", Type: meta.Of("mysql", "bigint", 0, 0, 0), HasDefault: true, PrimaryKey: true},
				{Name: "email", Type: meta.Of("mysql", "varchar", 255, 0, 0), Comment: "Unique login address"},
				{Name: "balance", Type: meta.Of("mysql", "decimal", 0, 10, 2), HasDefault: true, DefaultExpr: sql.Raw("0.00")},
			},
			Indexes: []meta.Index{
				{Name: "uq_users_email", Columns: []string{"email"}, Unique: true},
			},
			Checks: []meta.Check{
				{Name: "users_chk_1", Expression: sql.Raw("`balance` >= 0")},
			},
		},
		{
			Schema: meta.Schema{Name: "app"},
			Name:   "posts",
			Columns: []meta.Column{
				{Name: "id", Type: meta.Of("mysql", "bigint", 0, 0, 0), HasDefault: true, PrimaryKey: true},
				{Name: "user_id", Type: meta.Of("mysql", "bigint", 0, 0, 0)},
			},
			ForeignKeys: []meta.ForeignKey{
				{Name: "fk_posts_user", Columns: []string{"user_id"}, RefSchema: meta.Schema{Name: "app"}, RefTable: "users", RefColumns: []string{"id"}, OnDelete: meta.Cascade},
			},
		},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Schema() = %#v,\nwant %#v", got, want)
	}

	queries := fake.Queries()
	if len(queries) != 4 {
		t.Fatalf("queries = %d, want 4 (columns, foreign keys, indexes, checks)", len(queries))
	}
	for _, query := range queries {
		if !reflect.DeepEqual(query.Args, []any{"app"}) {
			t.Fatalf("query args = %#v, want schema bind", query.Args)
		}
	}
}
