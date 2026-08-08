package problem

import (
	"errors"
	"fmt"
)

var ErrNotFound = errors.New("problem not found")

type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}
