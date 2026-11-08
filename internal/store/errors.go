package store

import "errors"

// ErrNotFound is returned for unknown server IDs.
var ErrNotFound = errors.New("server not found")

// ErrDuplicateName is wrapped by the error Add and Update return when
// another server already uses the same name (case-insensitive).
var ErrDuplicateName = errors.New("duplicate server name")

// FieldError is a validation error for one field. Field is the JSON name
// of the field (e.g. "host", "poll_interval") so forms can highlight it;
// Error returns a message suitable for display, e.g.
// `host: "foo bar" is not a valid hostname or IP`.
type FieldError struct {
	Field string
	Msg   string
	// Err is an optional sentinel such as ErrDuplicateName.
	Err error
}

// Error implements error.
func (e *FieldError) Error() string { return e.Field + ": " + e.Msg }

// Unwrap returns the wrapped sentinel, if any.
func (e *FieldError) Unwrap() error { return e.Err }

func fieldErr(field, msg string) error {
	return &FieldError{Field: field, Msg: msg}
}
