package appstore

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Verifier validates StoreKit 2 / App Store Server Notification JWS payloads:
// it checks the x5c certificate chain against trusted Apple roots and verifies
// the ES256 signature locally (no per-transaction App Store API call).
type Verifier struct {
	roots          *x509.CertPool
	expectedBundle string // optional; if set, transaction bundleId must match
}

// NewVerifier trusts the given root certificate(s) (normally Apple Root CA -
// G3). expectedBundleID is optional bundle-id pinning ("" disables the check).
func NewVerifier(roots []*x509.Certificate, expectedBundleID string) (*Verifier, error) {
	if len(roots) == 0 {
		return nil, errors.New("appstore: at least one root certificate required")
	}
	pool := x509.NewCertPool()
	for _, c := range roots {
		pool.AddCert(c)
	}
	return &Verifier{roots: pool, expectedBundle: expectedBundleID}, nil
}

// NewVerifierFromFile loads trusted root certificate(s) from a PEM or DER file
// (e.g. Apple Root CA - G3 from apple.com/certificateauthority).
func NewVerifierFromFile(path, expectedBundleID string) (*Verifier, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("appstore: read root ca: %w", err)
	}
	certs, err := parseCertificates(raw)
	if err != nil {
		return nil, fmt.Errorf("appstore: parse root ca: %w", err)
	}
	return NewVerifier(certs, expectedBundleID)
}

// VerifyTransaction verifies a signed JWSTransaction and returns its payload.
func (v *Verifier) VerifyTransaction(signedJWS string) (*TransactionPayload, error) {
	payload := &TransactionPayload{}
	if err := v.parse(signedJWS, payload); err != nil {
		return nil, err
	}
	if v.expectedBundle != "" && payload.BundleID != v.expectedBundle {
		return nil, fmt.Errorf("appstore: bundle id mismatch: got %q want %q", payload.BundleID, v.expectedBundle)
	}
	return payload, nil
}

// VerifyNotification verifies an App Store Server Notification V2 signedPayload
// and, when present, its nested signed transaction.
func (v *Verifier) VerifyNotification(signedPayload string) (*NotificationPayload, *TransactionPayload, error) {
	notif := &NotificationPayload{}
	if err := v.parse(signedPayload, notif); err != nil {
		return nil, nil, err
	}

	var tx *TransactionPayload
	if notif.Data.SignedTransactionInfo != "" {
		var err error
		tx, err = v.VerifyTransaction(notif.Data.SignedTransactionInfo)
		if err != nil {
			return nil, nil, fmt.Errorf("appstore: notification transaction: %w", err)
		}
	}
	return notif, tx, nil
}

// parse verifies the JWS x5c chain + ES256 signature, unmarshalling the payload.
func (v *Verifier) parse(tokenString string, claims jwt.Claims) error {
	_, err := jwt.ParseWithClaims(tokenString, claims, v.keyFunc, jwt.WithValidMethods([]string{"ES256"}))
	return err
}

// keyFunc extracts the x5c chain, verifies it to a trusted root, and returns
// the leaf certificate's public key for signature verification.
func (v *Verifier) keyFunc(t *jwt.Token) (interface{}, error) {
	rawChain, ok := t.Header["x5c"].([]interface{})
	if !ok || len(rawChain) == 0 {
		return nil, errors.New("appstore: missing x5c header")
	}

	certs := make([]*x509.Certificate, 0, len(rawChain))
	for _, raw := range rawChain {
		s, ok := raw.(string)
		if !ok {
			return nil, errors.New("appstore: malformed x5c entry")
		}
		der, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return nil, fmt.Errorf("appstore: x5c base64: %w", err)
		}
		c, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, fmt.Errorf("appstore: x5c parse: %w", err)
		}
		certs = append(certs, c)
	}

	leaf := certs[0]
	intermediates := x509.NewCertPool()
	for _, c := range certs[1:] {
		intermediates.AddCert(c)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:         v.roots,
		Intermediates: intermediates,
		CurrentTime:   time.Now(),
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}); err != nil {
		return nil, fmt.Errorf("appstore: x5c chain verify: %w", err)
	}

	pub, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return nil, errors.New("appstore: leaf public key is not ECDSA")
	}
	return pub, nil
}
