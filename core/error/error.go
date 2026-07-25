package errs

import (
	"encoding/json"
	"fmt"
	"runtime"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type ErrCode interface {
	String() string
	Status() int
	MarshalText() ([]byte, error)
}

// Error represent an http handler error. FuncName/FileName are for logs
// only — never serialized to the client.
type Error struct {
	Code     ErrCode `json:"code"`
	Message  string  `json:"message"`
	FuncName string  `json:"-"`
	FileName string  `json:"-"`
}

// New constructs an error based on an app error.
func New(code ErrCode, err error) *Error {
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

// Status return the HTTPStatus of this error.
func (e *Error) Status() int {
	return e.Code.Status()
}

func (e *Error) Encode() ([]byte, string, error) {
	data, err := json.Marshal(e)
	return data, "application/json", err
}

func (e *Error) GRPCStatus() *status.Status {
	s := status.New(codes.Code(e.Code.Status()), e.Error())
	ds, err := s.WithDetails(&errdetails.ResourceInfo{
		ResourceType: e.FuncName,
		ResourceName: e.FileName,
	})
	if err != nil {
		return s // WithDetails failed; degrade to bare status
	}
	return ds
}
