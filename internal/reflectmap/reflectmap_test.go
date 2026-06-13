package reflectmap

import (
	"reflect"
	"testing"
	"time"
)

func TestFieldsByColumn(t *testing.T) {
	type Embedded struct {
		CreatedAt time.Time
	}
	type Row struct {
		Embedded
		ID     int64 `sqlkit:"id"`
		UserID int64
		Skip   string `sqlkit:"-"`
		hidden int    //nolint:unused // unexported, must be ignored
	}

	fields := FieldsByColumn(reflect.TypeOf(Row{}))

	cases := map[string]bool{
		"id":         true,  // explicit tag
		"userid":     true,  // lowercased field name
		"user_id":    true,  // snake_case field name
		"created_at": true,  // promoted from embedded struct
		"skip":       false, // tag "-"
		"hidden":     false, // unexported
	}
	for col, want := range cases {
		_, got := fields[col]
		if got != want {
			t.Errorf("FieldsByColumn[%q] present = %v, want %v", col, got, want)
		}
	}
}

func TestMapsAsStruct(t *testing.T) {
	if MapsAsStruct(reflect.TypeOf(time.Time{})) {
		t.Error("time.Time should not map as a struct")
	}
	if !MapsAsStruct(reflect.TypeOf(struct{ X int }{})) {
		t.Error("plain struct should map as a struct")
	}
	if MapsAsStruct(reflect.TypeOf(0)) {
		t.Error("scalar should not map as a struct")
	}
}
