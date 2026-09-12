package sms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"mokapay.app/core/logger"
)

const (
	// Twilio Programmable Messaging. There is no sandbox host: the test
	// credentials (test Account SID + test Auth Token, magic From number
	// +15005550006) go through this very endpoint and never deliver.
	twilioMessagesURL = "https://api.twilio.com/2010-04-01/Accounts/%s/Messages.json"

	twilioTimeout      = 15 * time.Second
	twilioMaxRespBytes = 1 << 16 // one message resource, never more
)

// Message statuses Twilio returns for "we took it". Anything else —
// "failed", "undelivered", "canceled" or a status we do not know — is a
// rejection.
const (
	twilioStatusQueued    = "queued"
	twilioStatusAccepted  = "accepted"
	twilioStatusSending   = "sending"
	twilioStatusSent      = "sent"
	twilioStatusScheduled = "scheduled"
)

// Twilio delivers through Twilio's Programmable Messaging API. One message
// per call.
//
// Note it never logs the message body: every SMS this service sends carries
// an OTP code.
type Twilio struct {
	log                 *logger.Logger
	accountSID          string
	authToken           string
	from                string // E.164 number or alphanumeric sender id
	messagingServiceSID string // preferred in production: sender pool + compliance
	url                 string
	client              *http.Client
}

// NewTwilio builds the live sender. accountSID and authToken are required —
// an API Key SID / Secret pair works in those two slots just as well as the
// account credentials. Exactly one of from and messagingServiceSID must be
// set: Twilio rejects a request carrying both or neither, and we would
// rather fail at startup than on the first real OTP.
func NewTwilio(log *logger.Logger, accountSID, authToken, from, messagingServiceSID string) (Twilio, error) {
	if accountSID == "" {
		return Twilio{}, errors.New("sms: twilio account sid is required")
	}
	if authToken == "" {
		return Twilio{}, errors.New("sms: twilio auth token is required")
	}
	if from == "" && messagingServiceSID == "" {
		return Twilio{}, errors.New("sms: twilio needs either a from or a messaging service sid")
	}
	if from != "" && messagingServiceSID != "" {
		return Twilio{}, errors.New("sms: twilio takes a from or a messaging service sid, not both")
	}

	return Twilio{
		log:                 log,
		accountSID:          accountSID,
		authToken:           authToken,
		from:                from,
		messagingServiceSID: messagingServiceSID,
		url:                 fmt.Sprintf(twilioMessagesURL, url.PathEscape(accountSID)),
		client:              &http.Client{Timeout: twilioTimeout},
	}, nil
}

// Send implements Sender.
func (t Twilio) Send(ctx context.Context, phoneNumber, message string) error {
	form := url.Values{
		"To":   {phoneNumber},
		"Body": {message},
	}
	// The constructor guarantees exactly one of the two is set; an empty one
	// must stay absent rather than be sent blank, which Twilio rejects.
	if t.messagingServiceSID != "" {
		form.Set("MessagingServiceSid", t.messagingServiceSID)
	} else {
		form.Set("From", t.from)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("sms: building request: %w", err)
	}
	req.SetBasicAuth(t.accountSID, t.authToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("sms: calling twilio: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.log.Error(ctx, "sms.twilio", "msg", "failed to close response body", "error", err)
		}
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, twilioMaxRespBytes))
	if err != nil {
		return fmt.Errorf("sms: reading response: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		// Twilio reports the reason in a JSON error resource; a gateway or
		// proxy in between may not, hence the raw-body fallback.
		var fail twilioError
		if err := json.Unmarshal(body, &fail); err != nil || fail.Code == 0 {
			return fmt.Errorf("%w: http %d: %s", ErrSendFailed, resp.StatusCode, strings.TrimSpace(string(body)))
		}
		return fmt.Errorf("%w: http %d: twilio %d: %s", ErrSendFailed, resp.StatusCode, fail.Code, strings.TrimSpace(fail.Message))
	}

	var out twilioResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return fmt.Errorf("sms: decoding response: %w", err)
	}

	// A 2xx still carries the outcome: an error code on the message resource
	// means Twilio created it and refused to deliver.
	if out.ErrorCode != nil {
		return fmt.Errorf("%w: %s: twilio %d: %s", ErrSendFailed, out.Status, *out.ErrorCode, strings.TrimSpace(out.ErrorMessage))
	}
	if !twilioAccepted(out.Status) {
		return fmt.Errorf("%w: %s: %s", ErrSendFailed, out.Status, strings.TrimSpace(out.ErrorMessage))
	}

	t.log.Info(ctx, "sms.twilio", "status", "accepted", "to", phoneNumber, "sid", out.SID)
	return nil
}

// twilioResponse is the message resource returned on a 2xx. Field names are
// theirs; ErrorCode is a pointer because null means "nothing wrong yet".
type twilioResponse struct {
	SID          string `json:"sid"`
	Status       string `json:"status"`
	ErrorCode    *int   `json:"error_code"`
	ErrorMessage string `json:"error_message"`
}

// twilioError is the body Twilio returns on a non-2xx.
type twilioError struct {
	Code     int    `json:"code"`
	Message  string `json:"message"`
	MoreInfo string `json:"more_info"`
	Status   int    `json:"status"`
}

func twilioAccepted(status string) bool {
	switch status {
	case twilioStatusQueued, twilioStatusAccepted, twilioStatusSending, twilioStatusSent, twilioStatusScheduled:
		return true
	}
	return false
}
