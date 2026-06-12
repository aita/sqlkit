package loader

import (
	"context"
	"database/sql/driver"
	"reflect"
	"strings"
	"testing"

	"github.com/sqlkit-dev/sqlkit/codegen"
	"github.com/sqlkit-dev/sqlkit/internal/sqltest"
)

func TestLoadRejectsUnsupportedDialects(t *testing.T) {
	if _, err := Load(context.Background(), nil, "oracle", ""); err == nil {
		t.Fatal("Load(oracle) succeeded, want unsupported error")
	}
}

func TestLoadPostgres(t *testing.T) {
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
					Columns: []string{"table_name", "column_name", "udt_name", "nullable", "has_default"},
					Rows: [][]driver.Value{
						{"users", "id", "int8", false, true},
						{"users", "email", "varchar", false, false},
						{"posts", "id", "int8", false, true},
						{"posts", "user_id", "int8", false, false},
					},
				}, nil
			}
		},
	}

	schema, err := Load(context.Background(), fake.DB(), "postgres", "public")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	want := codegen.Schema{
		Name:    "public",
		Dialect: "postgres",
		Tables: []codegen.Table{
			{
				Schema: "public",
				Name:   "users",
				Columns: []codegen.Column{
					{Name: "id", DataType: "int8", HasDefault: true, PrimaryKey: true},
					{Name: "email", DataType: "varchar"},
				},
			},
			{
				Schema: "public",
				Name:   "posts",
				Columns: []codegen.Column{
					{Name: "id", DataType: "int8", HasDefault: true, PrimaryKey: true},
					{Name: "user_id", DataType: "int8"},
				},
				ForeignKeys: []codegen.ForeignKey{
					{Name: "posts_user_id_fkey", Columns: []string{"user_id"}, RefSchema: "public", RefTable: "users", RefColumns: []string{"id"}},
				},
			},
		},
	}
	if !reflect.DeepEqual(schema, want) {
		t.Fatalf("Load() = %#v,\nwant %#v", schema, want)
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

func TestLoadMySQL(t *testing.T) {
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
					Columns: []string{"table_name", "column_name", "data_type", "nullable", "has_default", "is_pk"},
					Rows: [][]driver.Value{
						{"users", "id", "bigint", false, true, true},
						{"users", "email", "varchar", false, false, false},
						{"posts", "id", "bigint", false, true, true},
						{"posts", "user_id", "bigint", false, false, false},
					},
				}, nil
			}
		},
	}

	schema, err := Load(context.Background(), fake.DB(), "mysql", "app")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	want := codegen.Schema{
		Name:    "app",
		Dialect: "mysql",
		Tables: []codegen.Table{
			{
				Schema: "app",
				Name:   "users",
				Columns: []codegen.Column{
					{Name: "id", DataType: "bigint", HasDefault: true, PrimaryKey: true},
					{Name: "email", DataType: "varchar"},
				},
			},
			{
				Schema: "app",
				Name:   "posts",
				Columns: []codegen.Column{
					{Name: "id", DataType: "bigint", HasDefault: true, PrimaryKey: true},
					{Name: "user_id", DataType: "bigint"},
				},
				ForeignKeys: []codegen.ForeignKey{
					{Name: "fk_posts_user", Columns: []string{"user_id"}, RefSchema: "app", RefTable: "users", RefColumns: []string{"id"}},
				},
			},
		},
	}
	if !reflect.DeepEqual(schema, want) {
		t.Fatalf("Load() = %#v,\nwant %#v", schema, want)
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
