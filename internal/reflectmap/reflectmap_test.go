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

func TestFieldsParsesTagOptions(t *testing.T) {
	type Row struct {
		ID      int64          `sqlkit:"id"`
		Meta    map[string]any `sqlkit:"meta,json"`
		Raw     []byte         `sqlkit:"raw,jsonb"`
		Rev     int64          `sqlkit:",version"`
		Created int64          `sqlkit:"created,readonly"`
		Skip    string         `sqlkit:"-"`
		Loud    string         `sqlkit:"-,readonly"` // "-,opt" falls back to the field name
	}

	fields := Fields(reflect.TypeOf(Row{}))

	if f := fields["meta"]; !f.JSON {
		t.Errorf("meta.JSON = false, want true")
	}
	if f := fields["raw"]; !f.JSON {
		t.Errorf("raw (jsonb).JSON = false, want true")
	}
	if f := fields["rev"]; !f.Version { // empty name -> field name "rev"
		t.Errorf("rev.Version = false, want true")
	}
	if f := fields["created"]; !f.ReadOnly {
		t.Errorf("created.ReadOnly = false, want true")
	}
	if f := fields["id"]; f.JSON || f.Version || f.ReadOnly {
		t.Errorf("id has unexpected options: %+v", f)
	}
	if _, ok := fields["skip"]; ok {
		t.Errorf("skip should be excluded by the %q tag", "-")
	}
	if f, ok := fields["loud"]; !ok || !f.ReadOnly {
		t.Errorf(`"-,readonly" should map by field name with ReadOnly set, got %+v ok=%v`, f, ok)
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
