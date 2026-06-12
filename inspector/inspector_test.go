package inspector

import (
	"context"
	"database/sql/driver"
	"reflect"
	"strings"
	"testing"

	"github.com/aita/sqlkit/internal/sqltest"
	"github.com/aita/sqlkit/schema"
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
			default:
				return sqltest.Result{
					Columns: []string{"table_name", "column_name", "udt_name", "nullable", "has_default", "length", "precision", "scale"},
					Rows: [][]driver.Value{
						{"users", "id", "int8", false, true, 0, 64, 0},
						{"users", "email", "varchar", false, false, 255, 0, 0},
						{"users", "balance", "numeric", false, false, 0, 10, 2},
						{"posts", "id", "int8", false, true, 0, 64, 0},
						{"posts", "user_id", "int8", false, false, 0, 64, 0},
					},
				}, nil
			}
		},
	}

	got, err := Inspect(context.Background(), fake.DB(), "postgres", "public")
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}

	want := schema.Schema{
		Name:    "public",
		Dialect: "postgres",
		Tables: []schema.Table{
			{
				Schema: "public",
				Name:   "users",
				Columns: []schema.Column{
					{Name: "id", DataType: "int8", HasDefault: true, PrimaryKey: true},
					{Name: "email", DataType: "varchar", Length: 255},
					{Name: "balance", DataType: "numeric", Precision: 10, Scale: 2},
				},
			},
			{
				Schema: "public",
				Name:   "posts",
				Columns: []schema.Column{
					{Name: "id", DataType: "int8", HasDefault: true, PrimaryKey: true},
					{Name: "user_id", DataType: "int8"},
				},
				ForeignKeys: []schema.ForeignKey{
					{Name: "posts_user_id_fkey", Columns: []string{"user_id"}, RefSchema: "public", RefTable: "users", RefColumns: []string{"id"}},
				},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Inspect() = %#v,\nwant %#v", got, want)
	}

	queries := fake.Queries()
	if len(queries) != 3 {
		t.Fatalf("queries = %d, want 3 (columns, primary keys, foreign keys)", len(queries))
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
			default:
				return sqltest.Result{
					Columns: []string{"table_name", "column_name", "data_type", "nullable", "has_default", "is_pk", "length", "precision", "scale"},
					Rows: [][]driver.Value{
						{"users", "id", "bigint", false, true, true, 0, 19, 0},
						{"users", "email", "varchar", false, false, false, 255, 0, 0},
						{"users", "balance", "decimal", false, false, false, 0, 10, 2},
						{"posts", "id", "bigint", false, true, true, 0, 19, 0},
						{"posts", "user_id", "bigint", false, false, false, 0, 19, 0},
					},
				}, nil
			}
		},
	}

	got, err := Inspect(context.Background(), fake.DB(), "mysql", "app")
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}

	want := schema.Schema{
		Name:    "app",
		Dialect: "mysql",
		Tables: []schema.Table{
			{
				Schema: "app",
				Name:   "users",
				Columns: []schema.Column{
					{Name: "id", DataType: "bigint", HasDefault: true, PrimaryKey: true},
					{Name: "email", DataType: "varchar", Length: 255},
					{Name: "balance", DataType: "decimal", Precision: 10, Scale: 2},
				},
			},
			{
				Schema: "app",
				Name:   "posts",
				Columns: []schema.Column{
					{Name: "id", DataType: "bigint", HasDefault: true, PrimaryKey: true},
					{Name: "user_id", DataType: "bigint"},
				},
				ForeignKeys: []schema.ForeignKey{
					{Name: "fk_posts_user", Columns: []string{"user_id"}, RefSchema: "app", RefTable: "users", RefColumns: []string{"id"}},
				},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Inspect() = %#v,\nwant %#v", got, want)
	}

	queries := fake.Queries()
	if len(queries) != 2 {
		t.Fatalf("queries = %d, want 2 (columns, foreign keys)", len(queries))
	}
	for _, query := range queries {
		if !reflect.DeepEqual(query.Args, []any{"app"}) {
			t.Fatalf("query args = %#v, want schema bind", query.Args)
		}
	}
}
