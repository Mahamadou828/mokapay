package usecase

import (
	"errors"
	"fmt"
	"strings"

	"github.com/nyaruka/phonenumbers"
)

const DefaultRegion = "CD" // DR Congo

var (
	ErrUnparseable = errors.New("phone: cannot parse")
	ErrInvalid     = errors.New("phone: not a valid number")
	ErrNotMobile   = errors.New("phone: not a mobile number")
	ErrWrongRegion = errors.New("phone: unsupported country")
)

// normalizePhoneNumber converts user input to E.164 ("+243812345678").
func normalizePhoneNumber(input string) (string, error) {
	num, err := phonenumbers.Parse(clean(input), DefaultRegion)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnparseable, err)
	}

	// Parse is lenient — it only checks the input LOOKS like a number. This is
	// the call that checks the digits fall in a real allocated range.
	if !phonenumbers.IsValidNumber(num) {
		return "", ErrInvalid
	}

	switch phonenumbers.GetNumberType(num) {
	case phonenumbers.MOBILE:
	case phonenumbers.FIXED_LINE_OR_MOBILE:
		// Must accept this. For many countries — much of Africa included —
		// libphonenumber's metadata cannot distinguish the two, so demanding
		// a strict MOBILE rejects large numbers of valid mobile subscribers.
	default:
		return "", ErrNotMobile
	}

	return phonenumbers.Format(num, phonenumbers.E164), nil
}

func clean(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "+") {
		return s
	}

	var digits strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	d := digits.String()

	cc := fmt.Sprintf("%d", phonenumbers.GetCountryCodeForRegion(DefaultRegion))
	if strings.HasPrefix(d, cc) && len(d) > len(cc)+6 {
		return "+" + d
	}
	return s
}
