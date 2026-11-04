package introspection

import (
	"context"
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
			case strings.Contains(sql, "pg_enum"):
				return testutil.Result{
					Columns: []string{"typname", "enumlabel"},
					Rows: [][]driver.Value{
						{"mood", "happy"},
						{"mood", "sad"},
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

	want := meta.Metadata{
		Enums: []meta.Enum{{Schema: "public", Name: "mood", Values: []string{"happy", "sad"}}},
		Tables: []meta.Table{
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
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Schema() = %#v,\nwant %#v", got, want)
	}

	queries := fake.Queries()
	if len(queries) != 7 {
		t.Fatalf("queries = %d, want 7 (columns, primary keys, foreign keys, indexes, checks, comments, enums)", len(queries))
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

// TestInspectPostgresEnumOrderAndDedup covers two enum types with labels
// grouped and returned in enumsortorder (not alphabetical, not row order
// across types), and confirms a schema with no enum types at all leaves
// Metadata.Enums nil rather than an empty non-nil slice.
func TestInspectPostgresEnumOrderAndDedup(t *testing.T) {
	fake := &testutil.Fake{
		QueryFunc: func(sql string, args []any) (testutil.Result, error) {
			if strings.Contains(sql, "pg_enum") {
				return testutil.Result{
					Columns: []string{"typname", "enumlabel"},
					Rows: [][]driver.Value{
						{"mood", "sad"},
						{"status", "on"},
						{"mood", "happy"},
						{"status", "off"},
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
	want := []meta.Enum{
		{Schema: "public", Name: "mood", Values: []string{"sad", "happy"}},
		{Schema: "public", Name: "status", Values: []string{"on", "off"}},
	}
	if !reflect.DeepEqual(got.Enums, want) {
		t.Fatalf("Enums = %#v,\nwant %#v", got.Enums, want)
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
