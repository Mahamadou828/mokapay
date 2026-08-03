package email

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wneessen/go-mail"
	"mokapay.app/core/logger"
)

// ErrSendFailed means the SMTP server refused the message (bad sender,
// unknown recipient, relay denied, …) or was unreachable. Callers use
// errors.Is to tell it apart from a message-construction error.
var ErrSendFailed = errors.New("email: smtp server refused the message")

const smtpTimeout = 15 * time.Second

// SMTP delivers through the business' SMTP server. One message per call.
//
// Note it never logs the body: every email this service sends carries an
// OTP code.
type SMTP struct {
	log    *logger.Logger
	client *mail.Client
	from   string
}

// NewSMTP builds the live sender. username may be empty, in which case the
// server is addressed unauthenticated (an internal relay); otherwise the
// mechanism is auto-discovered from what the server advertises. Port 465 is
// implicit TLS; every other port negotiates STARTTLS opportunistically,
// since the server may not offer TLS yet.
func NewSMTP(log *logger.Logger, host string, port int, username, password, from string) (SMTP, error) {
	opts := []mail.Option{
		mail.WithPort(port),
		mail.WithTimeout(smtpTimeout),
	}
	if port == 465 {
		opts = append(opts, mail.WithSSL())
	} else {
		opts = append(opts, mail.WithTLSPolicy(mail.TLSOpportunistic))
	}
	if username != "" {
		opts = append(opts,
			mail.WithSMTPAuth(mail.SMTPAuthAutoDiscover),
			mail.WithUsername(username),
			mail.WithPassword(password),
		)
	} else {
		opts = append(opts, mail.WithSMTPAuth(mail.SMTPAuthNoAuth))
	}

	client, err := mail.NewClient(host, opts...)
	if err != nil {
		return SMTP{}, fmt.Errorf("email: building smtp client: %w", err)
	}

	return SMTP{log: log, client: client, from: from}, nil
}

// Send implements Sender. The client dials and closes a connection per call,
// so concurrent sends are safe.
func (s SMTP) Send(ctx context.Context, email, subject, body string) error {
	msg := mail.NewMsg()
	if err := msg.From(s.from); err != nil {
		return fmt.Errorf("email: setting from %q: %w", s.from, err)
	}
	if err := msg.To(email); err != nil {
		return fmt.Errorf("email: setting recipient: %w", err)
	}
	msg.Subject(subject)
	msg.SetBodyString(mail.TypeTextPlain, body)

	if err := s.client.DialAndSendWithContext(ctx, msg); err != nil {
		return fmt.Errorf("%w: %w", ErrSendFailed, err)
	}

	s.log.Info(ctx, "email.smtp", "status", "accepted", "to", email, "subject", subject)
	return nil
}
