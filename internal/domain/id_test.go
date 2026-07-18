package domain

import (
	"testing"

	"github.com/google/uuid"
)

func TestNewIDReturnsUUIDv7(t *testing.T) {
	id, err := NewID()
	if err != nil {
		t.Fatalf("new ID: %v", err)
	}
	if id == uuid.Nil {
		t.Fatal("new ID returned nil UUID")
	}
	if id.Version() != uuid.Version(7) {
		t.Fatalf("ID version = %s, want VERSION_7", id.Version())
	}
}
