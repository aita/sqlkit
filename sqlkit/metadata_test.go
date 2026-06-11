package sqlkit_test

import (
	"testing"

	"github.com/sqlkit-dev/sqlkit/sqlkit/meta"
)

func TestTableMetadataQualifiedName(t *testing.T) {
	plain := meta.Table{Name: "users"}
	if got, want := plain.QualifiedName(), "users"; got != want {
		t.Fatalf("QualifiedName() = %q, want %q", got, want)
	}

	qualified := meta.Table{Schema: "public", Name: "users"}
	if got, want := qualified.QualifiedName(), "public.users"; got != want {
		t.Fatalf("QualifiedName() = %q, want %q", got, want)
	}
}

func TestMetadataColumnShape(t *testing.T) {
	metadata := meta.Database{Tables: []meta.Table{{
		Schema: "public",
		Name:   "users",
		Columns: []meta.Column{{
			Name:         "id",
			GoName:       "ID",
			DatabaseType: "bigint",
			GoType:       "int64",
			PrimaryKey:   true,
		}},
	}}}

	if len(metadata.Tables) != 1 || len(metadata.Tables[0].Columns) != 1 {
		t.Fatalf("unexpected metadata shape: %#v", metadata)
	}
	if !metadata.Tables[0].Columns[0].PrimaryKey {
		t.Fatal("expected primary key metadata to be retained")
	}
}
