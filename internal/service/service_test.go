package service

import (
	"errors"
	"testing"
)

func TestCursorIsOpaqueAndBoundToItsQuery(t *testing.T) {
	cursor, err := EncodeCursor("conversation:list", 20)
	if err != nil {
		t.Fatalf("EncodeCursor() error = %v", err)
	}
	offset, err := DecodeCursor(cursor, "conversation:list")
	if err != nil || offset != 20 {
		t.Fatalf("DecodeCursor() = %d, %v; want 20, nil", offset, err)
	}
	if _, err := DecodeCursor(cursor, "conversation:search"); !errors.Is(err, ErrCursorInvalid) {
		t.Fatalf("DecodeCursor() different query error = %v, want ErrCursorInvalid", err)
	}
	if _, err := DecodeCursor("not-a-cursor", "conversation:list"); !errors.Is(err, ErrCursorInvalid) {
		t.Fatalf("DecodeCursor() malformed error = %v, want ErrCursorInvalid", err)
	}
}
