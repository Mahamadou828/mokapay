// Package param provides helpers for reading typed query parameters.
package param

import (
	"fmt"
	"net/http"
)

// Enum reads an optional enum query parameter, looking it up in the generated
// <Enum>_value map. Returns nil when the param is absent, a pointer to the
// enum value when valid, and an error when the value is not a valid enum name.
func Enum[E ~int32](r *http.Request, name string, values map[string]int32) (*E, error) {
	s := r.URL.Query().Get(name)
	if s == "" {
		return nil, nil
	}
	v, ok := values[s]
	if !ok {
		return nil, fmt.Errorf("invalid %s: %q", name, s)
	}
	return new(E(v)), nil
}
