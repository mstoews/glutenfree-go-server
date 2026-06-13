// Package devsign creates a throwaway ECDSA certificate chain and signs JWS
// payloads the way Apple does (ES256 + x5c header), so the appstore verifier
// and the HTTP handlers can be exercised locally without TestFlight/sandbox.
//
// NOT FOR PRODUCTION. Real StoreKit transactions are signed by Apple; this only
// produces fixtures whose root must be trusted explicitly by the verifier.
package devsign

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Chain is a throwaway root -> intermediate -> leaf ECDSA chain.
type Chain struct {
	root    *x509.Certificate
	leafKey *ecdsa.PrivateKey
	x5c     []string // base64 DER, leaf first: [leaf, intermediate]
}

// NewChain builds a fresh root/intermediate/leaf chain.
func NewChain() (*Chain, error) {
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	interKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	now := time.Now().Add(-time.Hour)
	caTmpl := func(serial int64, cn string) *x509.Certificate {
		return &x509.Certificate{
			SerialNumber:          big.NewInt(serial),
			Subject:               pkix.Name{CommonName: cn},
			NotBefore:             now,
			NotAfter:              now.Add(72 * time.Hour),
			IsCA:                  true,
			BasicConstraintsValid: true,
			KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		}
	}

	root, err := selfSign(caTmpl(1, "GF Dev Root CA"), rootKey)
	if err != nil {
		return nil, err
	}
	inter, err := sign(caTmpl(2, "GF Dev Intermediate CA"), root, &interKey.PublicKey, rootKey)
	if err != nil {
		return nil, err
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "GF Dev Leaf"},
		NotBefore:    now,
		NotAfter:     now.Add(72 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}
	leaf, err := sign(leafTmpl, inter, &leafKey.PublicKey, interKey)
	if err != nil {
		return nil, err
	}

	return &Chain{
		root:    root,
		leafKey: leafKey,
		x5c: []string{
			base64.StdEncoding.EncodeToString(leaf.Raw),
			base64.StdEncoding.EncodeToString(inter.Raw),
		},
	}, nil
}

// RootPEM returns the chain's root certificate as PEM (for verifier config).
func (c *Chain) RootPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.root.Raw})
}

// SignJWS signs claims as an ES256 JWS carrying this chain's x5c header — the
// same shape Apple produces for signed transactions and notifications.
func (c *Chain) SignJWS(claims jwt.Claims) (string, error) {
	t := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	t.Header["x5c"] = c.x5c
	return t.SignedString(c.leafKey)
}

func selfSign(tmpl *x509.Certificate, key *ecdsa.PrivateKey) (*x509.Certificate, error) {
	return sign(tmpl, tmpl, &key.PublicKey, key)
}

func sign(tmpl, parent *x509.Certificate, pub *ecdsa.PublicKey, parentKey *ecdsa.PrivateKey) (*x509.Certificate, error) {
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, pub, parentKey)
	if err != nil {
		return nil, fmt.Errorf("devsign: create certificate: %w", err)
	}
	return x509.ParseCertificate(der)
}
