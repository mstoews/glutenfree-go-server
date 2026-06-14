// Package applesignin verifies "Sign in with Apple" identity tokens.
//
// An identity token is a JWT (RS256) issued by Apple. Verification checks the
// signature against Apple's published JWKS, the issuer, the audience (your
// app's bundle id), and expiry, then returns the stable user id (`sub`) and,
// when present, the email. Apple only includes email on the FIRST
// authorization, so callers should persist it then.
package applesignin

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// issuer is the fixed `iss` claim on every Apple identity token.
const issuer = "https://appleid.apple.com"

// Identity holds the verified claims from an Apple identity token.
type Identity struct {
	Subject        string // stable, app-scoped Apple user id (the `sub` claim)
	Email          string // present only on the first authorization
	EmailVerified  bool
	IsPrivateEmail bool // true when Apple's "Hide My Email" relay is used
}

// KeySource returns the RSA public key for a given key id (kid). The default
// implementation fetches and caches Apple's JWKS; tests inject a fixed key.
type KeySource interface {
	Key(ctx context.Context, kid string) (*rsa.PublicKey, error)
}

// Verifier validates Apple identity tokens for a single audience (bundle id).
type Verifier struct {
	audience string
	keys     KeySource
	now      func() time.Time
}

// NewVerifier builds a Verifier with an explicit key source (used in tests).
func NewVerifier(audience string, keys KeySource) *Verifier {
	return &Verifier{audience: audience, keys: keys, now: time.Now}
}

// NewAppleVerifier builds a Verifier backed by Apple's live JWKS endpoint.
func NewAppleVerifier(audience string) *Verifier {
	return NewVerifier(audience, NewAppleKeySource())
}

// Verify checks the token and returns the verified identity.
func (v *Verifier) Verify(ctx context.Context, idToken string) (Identity, error) {
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithIssuer(issuer),
		jwt.WithAudience(v.audience),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(v.now),
	)

	claims := jwt.MapClaims{}
	if _, err := parser.ParseWithClaims(idToken, claims, func(t *jwt.Token) (interface{}, error) {
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("apple token missing kid header")
		}
		return v.keys.Key(ctx, kid)
	}); err != nil {
		return Identity{}, fmt.Errorf("verify apple identity token: %w", err)
	}

	sub, _ := claims["sub"].(string)
	if sub == "" {
		return Identity{}, errors.New("apple token missing sub claim")
	}

	id := Identity{Subject: sub}
	if email, ok := claims["email"].(string); ok {
		id.Email = email
	}
	id.EmailVerified = claimBool(claims["email_verified"])
	id.IsPrivateEmail = claimBool(claims["is_private_email"])
	return id, nil
}

// Apple encodes these booleans inconsistently as either a JSON bool or the
// strings "true"/"false".
func claimBool(v interface{}) bool {
	switch b := v.(type) {
	case bool:
		return b
	case string:
		return b == "true"
	default:
		return false
	}
}
