package mailer

import (
	"context"

	"github.com/rs/zerolog/log"
)

// noopSender logs messages instead of delivering them. Used when no mail
// provider is configured (local dev, tests, and any deploy that has not set
// RESEND_API_KEY).
type noopSender struct{}

// NewNoop returns a Sender that logs and discards every message.
func NewNoop() Sender { return noopSender{} }

func (noopSender) Enabled() bool { return false }

func (noopSender) Send(_ context.Context, msg Message) error {
	// Logged at warn so a misconfigured deploy is visible, and the body is
	// included so a developer can copy the reset link out of the logs.
	log.Warn().
		Str("to", msg.To).
		Str("subject", msg.Subject).
		Str("body", msg.Text).
		Msg("mail not configured; message discarded")
	return nil
}
