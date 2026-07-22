// Package middleware provides a set of default middleware usable for multiple http project
package middleware

import (
	"fmt"
	"net/http"

	core "template.app/core/http"
)

// default set of middleware error code
var (
	// errInternal errors. Means some invariants expected by underlying
	// system has been broken. If you see one of these errors,
	// something is very broken.
	errInternal = errorCode{value: 14}

	// InternalOnlyLog errors. Means some invariants expected by underlying
	// system has been broken. If you see one of these errors,
	// something is very broken. The error message is not sent to the client.
	errInternalOnlyLog = errorCode{value: 19}
)

var codeNumbers = map[string]errorCode{
	"internal":          errInternal,
	"internal_only_log": errInternalOnlyLog,
}

var codeNames = map[errorCode]string{
	errInternal:        "internal",
	errInternalOnlyLog: "internal_only_log",
}

var httpStatus = map[errorCode]int{
	errInternal:        http.StatusInternalServerError,
	errInternalOnlyLog: http.StatusInternalServerError,
}

// errorCode implements http.ErrCode interface
type errorCode struct {
	value int
}

// String returns the string representation of the error code.
func (ec errorCode) String() string {
	return codeNames[ec]
}

// UnmarshalText implement the unmarshal interface for JSON conversions.
func (ec *errorCode) UnmarshalText(data []byte) error {
	errName := string(data)

	v, exists := codeNumbers[errName]
	if !exists {
		return fmt.Errorf("err code %q does not exist", errName)
	}

	*ec = v

	return nil
}

// MarshalText implement the marshal interface for JSON conversions.
func (ec errorCode) MarshalText() ([]byte, error) {
	return []byte(ec.String()), nil
}

// Equal provides support for the go-cmp package and testing.
func (ec errorCode) Equal(ec2 errorCode) bool {
	return ec.value == ec2.value
}

// HTTPStatus implements the web package httpStatus interface so the
// web framework can use the correct http status.
func (ec errorCode) HTTPStatus() int {
	return httpStatus[ec]
}

func checkIsError(e core.Encoder) error {
	err, hasError := e.(error)
	if hasError {
		return err
	}

	return err
}
