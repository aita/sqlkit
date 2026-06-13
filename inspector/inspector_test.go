package inspector

import (
	"context"
	"database/sql/driver"
	"reflect"
	"strings"
	"testing"

	"github.com/aita/sqlkit/internal/sqltest"
	"github.com/aita/sqlkit/meta"
)

func TestInspectRejectsUnsupportedDialects(t *testing.T) {
	if _, err := Inspect(context.Background(), nil, "oracle", ""); err == nil {
		t.Fatal("Inspect(oracle) succeeded, want unsupported error")
	}
}

func TestInspectPostgres(t *testing.T) {
	fake := &sqltest.Fake{
		QueryFunc: func(sql string, args []any) (sqltest.Result, error) {
			switch {
			case strings.Contains(sql, "FOREIGN KEY"):
				return sqltest.Result{
					Columns: []string{"constraint_name", "table_name", "column_name", "ref_schema", "ref_table", "ref_column"},
					Rows: [][]driver.Value{
						{"posts_user_id_fkey", "posts", "user_id", "public", "users", "id"},
					},
				}, nil
			case strings.Contains(sql, "PRIMARY KEY"):
				return sqltest.Result{
					Columns: []string{"table_name", "column_name"},
					Rows: [][]driver.Value{
						{"users", "id"},
						{"posts", "id"},
					},
				}, nil
			case strings.Contains(sql, "pg_index"):
				return sqltest.Result{
					Columns: []string{"table_name", "index_name", "unique", "column_name"},
					Rows: [][]driver.Value{
						{"users", "users_email_key", true, "email"},
						{"posts", "idx_posts_user_id", false, "user_id"},
					},
				}, nil
			case strings.Contains(sql, "pg_constraint"):
				return sqltest.Result{
					Columns: []string{"table_name", "constraint_name", "clause"},
					Rows: [][]driver.Value{
						{"users", "users_balance_check", "CHECK ((balance >= (0)::numeric))"},
					},
				}, nil
			case strings.Contains(sql, "pg_description"):
				return sqltest.Result{
					Columns: []string{"table_name", "column_name", "comment"},
					Rows: [][]driver.Value{
						{"users", "", "Registered accounts"},
						{"users", "email", "Unique login address"},
					},
				}, nil
			default:
				return sqltest.Result{
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

	got, err := Inspect(context.Background(), fake.DB(), "postgres", "public")
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}

	want := meta.Metadata{Tables: []meta.Table{
		{
			Schema:  meta.Schema{Name: "public"},
			Name:    "users",
			Comment: "Registered accounts",
			Columns: []meta.Column{
				{Name: "id", Type: meta.Of("postgres", "int8", 0, 0, 0), HasDefault: true, PrimaryKey: true},
				{Name: "email", Type: meta.Of("postgres", "varchar", 255, 0, 0), Comment: "Unique login address"},
				{Name: "balance", Type: meta.Of("postgres", "numeric", 0, 10, 2), HasDefault: true, DefaultExpr: "0"},
			},
			Indexes: []meta.Index{
				{Name: "users_email_key", Columns: []string{"email"}, Unique: true},
			},
			Checks: []meta.Check{
				{Name: "users_balance_check", Expression: "balance >= (0)::numeric"},
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
				{Name: "posts_user_id_fkey", Columns: []string{"user_id"}, RefSchema: meta.Schema{Name: "public"}, RefTable: "users", RefColumns: []string{"id"}},
			},
			Indexes: []meta.Index{
				{Name: "idx_posts_user_id", Columns: []string{"user_id"}},
			},
		},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Inspect() = %#v,\nwant %#v", got, want)
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

func TestInspectMySQL(t *testing.T) {
	fake := &sqltest.Fake{
		QueryFunc: func(sql string, args []any) (sqltest.Result, error) {
			switch {
			case strings.Contains(sql, "REFERENCED_TABLE_NAME"):
				return sqltest.Result{
					Columns: []string{"constraint_name", "table_name", "column_name", "ref_schema", "ref_table", "ref_column"},
					Rows: [][]driver.Value{
						{"fk_posts_user", "posts", "user_id", "app", "users", "id"},
					},
				}, nil
			case strings.Contains(sql, "STATISTICS"):
				return sqltest.Result{
					Columns: []string{"table_name", "index_name", "unique", "column_name"},
					Rows: [][]driver.Value{
						{"users", "uq_users_email", true, "email"},
					},
				}, nil
			case strings.Contains(sql, "CHECK_CONSTRAINTS"):
				return sqltest.Result{
					Columns: []string{"table_name", "constraint_name", "clause"},
					Rows: [][]driver.Value{
						{"users", "users_chk_1", "(`balance` >= 0)"},
					},
				}, nil
			default:
				return sqltest.Result{
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

	got, err := Inspect(context.Background(), fake.DB(), "mysql", "app")
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}

	want := meta.Metadata{Tables: []meta.Table{
		{
			Schema:  meta.Schema{Name: "app"},
			Name:    "users",
			Comment: "Registered accounts",
			Columns: []meta.Column{
				{Name: "id", Type: meta.Of("mysql", "bigint", 0, 0, 0), HasDefault: true, PrimaryKey: true},
				{Name: "email", Type: meta.Of("mysql", "varchar", 255, 0, 0), Comment: "Unique login address"},
				{Name: "balance", Type: meta.Of("mysql", "decimal", 0, 10, 2), HasDefault: true, DefaultExpr: "0.00"},
			},
			Indexes: []meta.Index{
				{Name: "uq_users_email", Columns: []string{"email"}, Unique: true},
			},
			Checks: []meta.Check{
				{Name: "users_chk_1", Expression: "`balance` >= 0"},
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
				{Name: "fk_posts_user", Columns: []string{"user_id"}, RefSchema: meta.Schema{Name: "app"}, RefTable: "users", RefColumns: []string{"id"}},
			},
		},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Inspect() = %#v,\nwant %#v", got, want)
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
