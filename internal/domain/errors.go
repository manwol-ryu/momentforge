// Package domain holds types shared by MomentForge packages. It has no
// dependencies on other internal packages and performs no I/O.
package domain

import "errors"

// UserError is an error whose Message can be shown to the user as is.
// Message says what happened and what to do next, in Korean.
type UserError struct {
	Code    string
	Message string
	Err     error
}

// NewUserError returns a UserError without an underlying cause.
func NewUserError(code, message string) *UserError {
	return &UserError{Code: code, Message: message}
}

// WrapUserError returns a UserError that keeps err for logs and errors.Is.
func WrapUserError(code, message string, err error) *UserError {
	return &UserError{Code: code, Message: message, Err: err}
}

func (e *UserError) Error() string {
	if e.Err != nil {
		return e.Code + ": " + e.Message + ": " + e.Err.Error()
	}
	return e.Code + ": " + e.Message
}

func (e *UserError) Unwrap() error { return e.Err }

// AsUserError finds a UserError in err's chain.
func AsUserError(err error) (*UserError, bool) {
	var ue *UserError
	if errors.As(err, &ue) {
		return ue, true
	}
	return nil, false
}
