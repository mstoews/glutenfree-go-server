package mailer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"time"
)

const resendEndpoint = "https://api.resend.com/emails"

// resendSender delivers mail through Resend's REST API. It speaks plain HTTP
// rather than the vendor SDK — the send payload is small and stable, and this
// keeps the dependency tree unchanged.
type resendSender struct {
	from   string
	apiKey string
	client *http.Client
}

// NewResend returns a Sender backed by Resend. fromEmail must be on a domain
// verified in the Resend account, or sends are rejected with a 403.
func NewResend(apiKey, fromEmail, fromName string) Sender {
	return &resendSender{
		from:   formatAddress(fromEmail, fromName),
		apiKey: apiKey,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// formatAddress renders "Name <addr@example.com>", which is how Resend takes a
// display name — it has no separate name field. mail.Address handles the
// quoting rules for names containing commas, quotes, and the like.
func formatAddress(email, name string) string {
	if name == "" {
		return email
	}
	addr := mail.Address{Name: name, Address: email}
	return addr.String()
}

func (s *resendSender) Enabled() bool { return true }

// resendPayload mirrors the subset of the send-email schema we use.
type resendPayload struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	Text    string   `json:"text,omitempty"`
	HTML    string   `json:"html,omitempty"`
}

func (s *resendSender) Send(ctx context.Context, msg Message) error {
	body, err := json.Marshal(resendPayload{
		From:    s.from,
		To:      []string{msg.To},
		Subject: msg.Subject,
		Text:    msg.Text,
		HTML:    msg.HTML,
	})
	if err != nil {
		return fmt.Errorf("encode resend payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, resendEndpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build resend request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	req.Header.Set("Content-Type", "application/json")

	res, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("send mail: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		// Cap the echoed error body; the JSON error can be verbose.
		detail, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return fmt.Errorf("resend returned %d: %s", res.StatusCode, bytes.TrimSpace(detail))
	}
	return nil
}
