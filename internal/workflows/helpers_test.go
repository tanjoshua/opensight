package workflows

import (
	"testing"

	"opensight/internal/domain"
)

func mustID(t *testing.T) domain.ID {
	t.Helper()
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
