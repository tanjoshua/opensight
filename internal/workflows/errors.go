package workflows

import (
	"errors"
	"fmt"
)

type PermanentError struct{ Err error }

func (e *PermanentError) Error() string { return e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }

func IsPermanent(err error) bool {
	var permanent *PermanentError
	return errors.As(err, &permanent)
}

func NewPermanentError(message, _ string, err error) error {
	return &PermanentError{Err: fmt.Errorf("%s: %w", message, err)}
}
