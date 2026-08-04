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

// ErrSendFailed means the provider accepted the HTTP call but refused to
// deliver (bad sender id, unroutable number, no credit, …). Callers use
// errors.Is to tell it apart from a transport failure.
var ErrSendFailed = errors.New("sms: provider refused the message")

const (
	// Africa's Talking Messaging API. The sandbox is selected by the
	// username — "sandbox" is a real account name on their side, not a flag.
	atProductionURL = "https://api.africastalking.com/version1/messaging/bulk"
	atSandboxURL    = "https://api.sandbox.africastalking.com/version1/messaging"
	atSandboxUser   = "sandbox"

	atTimeout      = 15 * time.Second
	atMaxRespBytes = 1 << 16 // enough for any recipient list we send (one)
)

// Recipient statusCodes Africa's Talking uses for "we took it": 100
// Processed, 101 Sent, 102 Queued. Anything else is a rejection.
const (
	atStatusProcessed = 100
	atStatusSent      = 101
	atStatusQueued    = 102
)

// AfricasTalking delivers through Africa's Talking, the SMS provider for our
// DRC routes. One message per call — the API is a bulk one, we just never
// pass more than a single recipient.
//
// Note it never logs the message body: every SMS this service sends carries
// an OTP code.
type AfricasTalking struct {
	log      *logger.Logger
	username string
	apiKey   string
	senderID string // optional alphanumeric sender; requires AT registration
	url      string
	client   *http.Client
}

// NewAfricasTalking builds the live sender. senderID may be empty, in which
// case Africa's Talking delivers from its shared pool — a dedicated one has
// to be registered and approved with them first.
func NewAfricasTalking(log *logger.Logger, username, apiKey, senderID string) AfricasTalking {
	endpoint := atProductionURL
	if username == atSandboxUser {
		endpoint = atSandboxURL
	}

	return AfricasTalking{
		log:      log,
		username: username,
		apiKey:   apiKey,
		senderID: senderID,
		url:      endpoint,
		client:   &http.Client{Timeout: atTimeout},
	}
}

// Send implements Sender.
func (a AfricasTalking) Send(ctx context.Context, phoneNumber, message string) error {
	form := url.Values{
		"username":     {a.username},
		"phoneNumbers": {phoneNumber},
		"message":      {message},
		"bulkSMSMode":  {"1"},
	}
	if a.senderID != "" {
		form.Set("senderId", a.senderID)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.url, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("sms: building request: %w", err)
	}
	req.Header.Set("apiKey", a.apiKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("sms: calling africa's talking: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			a.log.Error(ctx, "sms.africastalking", "msg", "failed to close response body", "error", err)
		}
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, atMaxRespBytes))
	if err != nil {
		return fmt.Errorf("sms: reading response: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("%w: http %d: %s", ErrSendFailed, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var out atResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return fmt.Errorf("sms: decoding response: %w", err)
	}

	// A 2xx with no recipient means the request itself was rejected (unknown
	// sender id, blacklisted destination, …); the reason is only in Message.
	if len(out.SMSMessageData.Recipients) == 0 {
		return fmt.Errorf("%w: %s", ErrSendFailed, strings.TrimSpace(out.SMSMessageData.Message))
	}

	for _, r := range out.SMSMessageData.Recipients {
		if !atAccepted(r.StatusCode) {
			return fmt.Errorf("%w: %s: %s (status %d)", ErrSendFailed, r.Number, r.Status, r.StatusCode)
		}
	}

	a.log.Info(ctx, "sms.africastalking", "status", "accepted", "to", phoneNumber, "detail", out.SMSMessageData.Message)
	return nil
}

// atResponse is the Messaging API reply. Field names are theirs.
type atResponse struct {
	SMSMessageData struct {
		Message    string        `json:"Message"`
		Recipients []atRecipient `json:"Recipients"`
	} `json:"SMSMessageData"`
}

type atRecipient struct {
	Number     string `json:"number"`
	Status     string `json:"status"`
	StatusCode int    `json:"statusCode"`
	Cost       string `json:"cost"`
	MessageID  string `json:"messageId"`
}

func atAccepted(statusCode int) bool {
	switch statusCode {
	case atStatusProcessed, atStatusSent, atStatusQueued:
		return true
	}
	return false
}
