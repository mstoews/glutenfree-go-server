package appstore

import (
	"strings"
	"testing"

	"github.com/mstoews/glutenfree-server/appstore/devsign"
)

// verifierFor builds a Verifier trusting the given dev chain's root.
func verifierFor(t *testing.T, c *devsign.Chain, bundle string) *Verifier {
	t.Helper()
	roots, err := parseCertificates(c.RootPEM())
	if err != nil {
		t.Fatalf("parse root: %v", err)
	}
	v, err := NewVerifier(roots, bundle)
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	return v
}

func sampleTxn() *TransactionPayload {
	return &TransactionPayload{
		TransactionID:         "100000000000001",
		OriginalTransactionID: "100000000000001",
		BundleID:              "com.glutenfree.app",
		ProductID:             "com.glutenfree.sub.monthly",
		ExpiresDateMS:         32503680000000, // year 3000
		Type:                  "Auto-Renewable Subscription",
		Environment:           "Sandbox",
	}
}

func TestVerifyTransaction_Valid(t *testing.T) {
	chain, err := devsign.NewChain()
	if err != nil {
		t.Fatal(err)
	}
	signed, err := chain.SignJWS(sampleTxn())
	if err != nil {
		t.Fatal(err)
	}

	v := verifierFor(t, chain, "com.glutenfree.app")
	got, err := v.VerifyTransaction(signed)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.OriginalTransactionID != "100000000000001" {
		t.Errorf("originalTransactionId = %q", got.OriginalTransactionID)
	}
	if got.ProductID != "com.glutenfree.sub.monthly" {
		t.Errorf("productId = %q", got.ProductID)
	}
	if got.ExpiresAt().IsZero() {
		t.Errorf("expiresAt should be set")
	}
}

func TestVerifyTransaction_WrongRoot(t *testing.T) {
	chain, _ := devsign.NewChain()
	other, _ := devsign.NewChain()
	signed, err := chain.SignJWS(sampleTxn())
	if err != nil {
		t.Fatal(err)
	}
	// Verifier trusts a different chain's root -> chain verification must fail.
	v := verifierFor(t, other, "")
	if _, err := v.VerifyTransaction(signed); err == nil {
		t.Fatal("expected error for untrusted root, got nil")
	}
}

func TestVerifyTransaction_Tampered(t *testing.T) {
	chain, _ := devsign.NewChain()
	signed, err := chain.SignJWS(sampleTxn())
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(signed, ".")
	if len(parts) != 3 {
		t.Fatalf("unexpected JWS segments: %d", len(parts))
	}
	// Flip a character in the payload segment; signature no longer matches.
	b := []byte(parts[1])
	if b[0] == 'A' {
		b[0] = 'B'
	} else {
		b[0] = 'A'
	}
	parts[1] = string(b)
	tampered := strings.Join(parts, ".")

	v := verifierFor(t, chain, "")
	if _, err := v.VerifyTransaction(tampered); err == nil {
		t.Fatal("expected error for tampered payload, got nil")
	}
}

func TestVerifyTransaction_BundleMismatch(t *testing.T) {
	chain, _ := devsign.NewChain()
	signed, err := chain.SignJWS(sampleTxn())
	if err != nil {
		t.Fatal(err)
	}
	v := verifierFor(t, chain, "com.someone.else")
	if _, err := v.VerifyTransaction(signed); err == nil {
		t.Fatal("expected bundle-id mismatch error, got nil")
	}
}

func TestVerifyNotification_Valid(t *testing.T) {
	chain, _ := devsign.NewChain()

	signedTx, err := chain.SignJWS(sampleTxn())
	if err != nil {
		t.Fatal(err)
	}
	notif := &NotificationPayload{
		NotificationType: "DID_RENEW",
		NotificationUUID: "uuid-1",
		Version:          "2.0",
		Data: NotificationData{
			BundleID:              "com.glutenfree.app",
			Environment:           "Sandbox",
			SignedTransactionInfo: signedTx,
		},
	}
	signedNotif, err := chain.SignJWS(notif)
	if err != nil {
		t.Fatal(err)
	}

	v := verifierFor(t, chain, "com.glutenfree.app")
	gotNotif, gotTx, err := v.VerifyNotification(signedNotif)
	if err != nil {
		t.Fatalf("verify notification: %v", err)
	}
	if gotNotif.NotificationType != "DID_RENEW" {
		t.Errorf("notificationType = %q", gotNotif.NotificationType)
	}
	if gotTx == nil || gotTx.OriginalTransactionID != "100000000000001" {
		t.Errorf("nested transaction not verified: %+v", gotTx)
	}
}
