// Package encoder provide an generic encoder that implement http.Encoder for proto messages
package protocodec

import (
	"fmt"
	"net/http"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	errs "template.app/app/services/gateway/internal/sdk/errs"
	corehttp "template.app/core/http"
)

var marshaler = protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}

// Proto implement Encoder and Decoder method for proto messages
type Proto struct {
	Message proto.Message
	// Status is the HTTP status code sent with the response; 0 means 200.
	Status int
}

// HTTPStatus implements the http package httpStatus interface so the
// server writes a valid status code for successful proto responses.
func (p Proto) HTTPStatus() int {
	if p.Status != 0 {
		return p.Status
	}
	return http.StatusOK
}

func (p Proto) Decode(data []byte) error {
	if err := protojson.Unmarshal(data, p.Message); err != nil {
		return fmt.Errorf("request: unable to read payload: %w", err)
	}
	return nil
}

func (p Proto) Encode() ([]byte, string, error) {
	b, err := marshaler.Marshal(p.Message)
	if err != nil {
		return nil, "application/json", corehttp.NewHTTPError(errs.FailedToMarshal, err)
	}

	return b, "application/json", nil
}
