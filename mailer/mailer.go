// Package mailer sends transactional email. The only message the API sends
// today is the operator password-reset link.
//
// Sender is an interface so the API can run without mail configured: NewNoop
// returns a sender that logs the message instead of delivering it, which keeps
// local development and tests free of network calls and API keys.
package mailer

import (
	"context"
	"fmt"
)

// Message is a single transactional email. Both bodies are sent; clients that
// cannot render HTML fall back to Text.
type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

// Sender delivers a Message. Implementations must be safe for concurrent use.
type Sender interface {
	Send(ctx context.Context, msg Message) error
	// Enabled reports whether mail is actually delivered. Handlers use it to
	// return 501 rather than silently swallowing a request the operator
	// expected to produce an email.
	Enabled() bool
}

// ErrNotConfigured is returned by the noop sender's Send.
var ErrNotConfigured = fmt.Errorf("mail is not configured")
