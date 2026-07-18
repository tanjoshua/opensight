package domain

import (
	"fmt"

	"github.com/google/uuid"
)

// ID is the canonical application identifier type. IDs are generated in the
// app layer as UUIDv7 so primary keys stay time-ordered without relying on
// database-version-specific UUID functions.
type ID = uuid.UUID

// NewID returns a new UUIDv7 identifier for persisted domain entities.
func NewID() (ID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("new uuidv7: %w", err)
	}
	return id, nil
}
