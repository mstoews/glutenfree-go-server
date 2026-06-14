package applesignin

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	testKid      = "test-key-1"
	testAudience = "com.glutenfree.com.GlutenFree"
	testSubject  = "001234.fedcba9876543210.0001"
)

// staticKeySource returns a single fixed key, regardless of kid match.
type staticKeySource struct {
	kid string
	key *rsa.PublicKey
}

func (s staticKeySource) Key(_ context.Context, kid string) (*rsa.PublicKey, error) {
	if kid != s.kid {
		return nil, jwt.ErrTokenUnverifiable
	}
	return s.key, nil
}

func newTestKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key
}

func signToken(t *testing.T, key *rsa.PrivateKey, kid string, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = kid
	s, err := tok.SignedString(key)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return s
}

func baseClaims() jwt.MapClaims {
	now := time.Now()
	return jwt.MapClaims{
		"iss":            issuer,
		"aud":            testAudience,
		"sub":            testSubject,
		"iat":            now.Add(-time.Minute).Unix(),
		"exp":            now.Add(time.Hour).Unix(),
		"email":          "user@example.com",
		"email_verified": "true",
	}
}

func newTestVerifier(key *rsa.PrivateKey) *Verifier {
	return NewVerifier(testAudience, staticKeySource{kid: testKid, key: &key.PublicKey})
}

func TestVerify_Valid(t *testing.T) {
	key := newTestKey(t)
	token := signToken(t, key, testKid, baseClaims())

	id, err := newTestVerifier(key).Verify(context.Background(), token)
	if err != nil {
		t.Fatalf("expected valid token, got error: %v", err)
	}
	if id.Subject != testSubject {
		t.Errorf("subject = %q, want %q", id.Subject, testSubject)
	}
	if id.Email != "user@example.com" {
		t.Errorf("email = %q, want user@example.com", id.Email)
	}
	if !id.EmailVerified {
		t.Error("expected email_verified=true")
	}
}

func TestVerify_Rejects(t *testing.T) {
	key := newTestKey(t)
	otherKey := newTestKey(t)

	cases := []struct {
		name  string
		token string
	}{
		{"wrong issuer", signToken(t, key, testKid, mutate(baseClaims(), "iss", "https://evil.example.com"))},
		{"wrong audience", signToken(t, key, testKid, mutate(baseClaims(), "aud", "com.someone.else"))},
		{"expired", signToken(t, key, testKid, mutate(baseClaims(), "exp", time.Now().Add(-time.Hour).Unix()))},
		{"missing sub", signToken(t, key, testKid, deleteClaim(baseClaims(), "sub"))},
		{"unknown kid", signToken(t, key, "other-kid", baseClaims())},
		{"wrong signing key", signToken(t, otherKey, testKid, baseClaims())},
	}

	v := newTestVerifier(key)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := v.Verify(context.Background(), tc.token); err == nil {
				t.Errorf("expected error for %s, got nil", tc.name)
			}
		})
	}
}

func mutate(c jwt.MapClaims, k string, v interface{}) jwt.MapClaims {
	c[k] = v
	return c
}

func deleteClaim(c jwt.MapClaims, k string) jwt.MapClaims {
	delete(c, k)
	return c
}
