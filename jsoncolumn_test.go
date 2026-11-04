package sqlkit

import (
	"testing"
)

func TestJsonScannerZerosOnNull(t *testing.T) {
	type Payload struct {
		X int    `json:"x"`
		Y string `json:"y"`
	}

	var dst Payload
	s := jsonScanner{dst: &dst}

	if err := s.Scan([]byte(`{"x":42,"y":"hello"}`)); err != nil {
		t.Fatalf("Scan JSON: %v", err)
	}
	if dst.X != 42 || dst.Y != "hello" {
		t.Fatalf("after JSON scan: got %+v", dst)
	}

	if err := s.Scan(nil); err != nil {
		t.Fatalf("Scan NULL: %v", err)
	}
	if dst.X != 0 || dst.Y != "" {
		t.Fatalf("after NULL scan destination must be zeroed, got %+v", dst)
	}
}

func TestJsonScannerZerosSliceOnNull(t *testing.T) {
	var dst []int
	s := jsonScanner{dst: &dst}

	if err := s.Scan([]byte(`[1,2,3]`)); err != nil {
		t.Fatalf("Scan JSON: %v", err)
	}
	if len(dst) != 3 {
		t.Fatalf("after JSON scan: got %v", dst)
	}

	if err := s.Scan(nil); err != nil {
		t.Fatalf("Scan NULL: %v", err)
	}
	if dst != nil {
		t.Fatalf("after NULL scan slice must be nil, got %v", dst)
	}
}
