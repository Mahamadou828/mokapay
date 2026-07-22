package http

import (
	"encoding/json"
	"fmt"
	"runtime"
)

type ErrCode interface {
	String() string
	HTTPStatus() int
	MarshalText() ([]byte, error)
}

// Error represent an http handler error
type Error struct {
	Code     ErrCode `json:"code"`
	Message  string  `json:"message"`
	FuncName string
	FileName string
}

// NewHTTPError constructs an error based on an app error.
func NewHTTPError(code ErrCode, err error) *Error {
	pc, filename, line, _ := runtime.Caller(1)

	return &Error{
		Code:     code,
		Message:  err.Error(),
		FuncName: runtime.FuncForPC(pc).Name(),
		FileName: fmt.Sprintf("%s:%d", filename, line),
	}
}

// Errorf constructs an error based on a error message.
func Errorf(code ErrCode, format string, v ...any) *Error {
	pc, filename, line, _ := runtime.Caller(1)

	return &Error{
		Code:     code,
		Message:  fmt.Sprintf(format, v...),
		FuncName: runtime.FuncForPC(pc).Name(),
		FileName: fmt.Sprintf("%s:%d", filename, line),
	}
}

// Error implements the error interface.
func (e *Error) Error() string {
	return e.Message
}

// HTTPStatus return the HTTPStatus of this error.
func (e *Error) HTTPStatus() int {
	return e.Code.HTTPStatus()
}

func (e *Error) Encode() ([]byte, string, error) {
	data, err := json.Marshal(e)
	return data, "application/json", err
}
