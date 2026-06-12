package loader

import (
	"context"
	"database/sql/driver"
	"reflect"
	"strings"
	"testing"

	"github.com/sqlkit-dev/sqlkit/internal/genmodel"
	"github.com/sqlkit-dev/sqlkit/internal/sqltest"
)

func TestLoadRejectsUnimplementedDialects(t *testing.T) {
	if _, err := Load(context.Background(), nil, "mysql", ""); err == nil {
		t.Fatal("Load(mysql) succeeded, want not-implemented error")
	}
	if _, err := Load(context.Background(), nil, "oracle", ""); err == nil {
		t.Fatal("Load(oracle) succeeded, want unsupported error")
	}
}

func TestLoad(t *testing.T) {
	fake := &sqltest.Fake{
		QueryFunc: func(sql string, args []any) (sqltest.Result, error) {
			if strings.Contains(sql, "PRIMARY KEY") {
				return sqltest.Result{
					Columns: []string{"table_name", "column_name"},
					Rows: [][]driver.Value{
						{"users", "id"},
					},
				}, nil
			}
			return sqltest.Result{
				Columns: []string{"table_name", "column_name", "udt_name", "nullable", "has_default"},
				Rows: [][]driver.Value{
					{"users", "id", "int8", false, true},
					{"users", "email", "varchar", false, false},
					{"users", "name", "text", true, false},
				},
			}, nil
		},
	}

	schema, err := Load(context.Background(), fake.DB(), "postgres", "public")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	want := genmodel.Schema{
		Name: "public",
		Tables: []genmodel.Table{{
			Schema: "public",
			Name:   "users",
			Columns: []genmodel.Column{
				{Name: "id", DataType: "int8", HasDefault: true, PrimaryKey: true},
				{Name: "email", DataType: "varchar"},
				{Name: "name", DataType: "text", Nullable: true},
			},
		}},
	}
	if !reflect.DeepEqual(schema, want) {
		t.Fatalf("Load() = %#v,\nwant %#v", schema, want)
	}

	queries := fake.Queries()
	if len(queries) != 2 {
		t.Fatalf("queries = %d, want 2", len(queries))
	}
	for _, query := range queries {
		if !reflect.DeepEqual(query.Args, []any{"public"}) {
			t.Fatalf("query args = %#v, want schema bind", query.Args)
		}
	}
}
