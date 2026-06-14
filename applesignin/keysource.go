package applesignin

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"
)

// appleKeysURL serves Apple's public signing keys (JWKS). Keys rotate, so the
// source refreshes on a TTL and on cache misses.
const appleKeysURL = "https://appleid.apple.com/auth/keys"

type appleKeySource struct {
	url    string
	client *http.Client
	ttl    time.Duration

	mu      sync.Mutex
	keys    map[string]*rsa.PublicKey
	fetched time.Time
}

// NewAppleKeySource returns a KeySource backed by Apple's JWKS endpoint, with a
// one-hour cache and stale-on-error fallback.
func NewAppleKeySource() KeySource {
	return &appleKeySource{
		url:    appleKeysURL,
		client: &http.Client{Timeout: 10 * time.Second},
		ttl:    time.Hour,
	}
}

func (s *appleKeySource) Key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if key, ok := s.keys[kid]; ok && time.Since(s.fetched) < s.ttl {
		return key, nil
	}

	if err := s.refresh(ctx); err != nil {
		// Serve a stale key if we have one — a transient JWKS outage shouldn't
		// fail an otherwise valid login.
		if key, ok := s.keys[kid]; ok {
			return key, nil
		}
		return nil, err
	}

	key, ok := s.keys[kid]
	if !ok {
		return nil, fmt.Errorf("apple signing key %q not found", kid)
	}
	return key, nil
}

type appleJWK struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func (s *appleKeySource) refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("apple jwks returned status %d", resp.StatusCode)
	}

	var doc struct {
		Keys []appleJWK `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return err
	}

	keys := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Kty != "RSA" {
			continue
		}
		pub, err := rsaPublicKeyFromJWK(k.N, k.E)
		if err != nil {
			continue
		}
		keys[k.Kid] = pub
	}
	if len(keys) == 0 {
		return errors.New("apple jwks contained no usable RSA keys")
	}

	s.keys = keys
	s.fetched = time.Now()
	return nil
}

// rsaPublicKeyFromJWK reconstructs an RSA public key from base64url modulus (n)
// and exponent (e).
func rsaPublicKeyFromJWK(nStr, eStr string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(nStr)
	if err != nil {
		return nil, fmt.Errorf("decode modulus: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(eStr)
	if err != nil {
		return nil, fmt.Errorf("decode exponent: %w", err)
	}

	e := 0
	for _, b := range eBytes {
		e = e<<8 | int(b)
	}
	if e == 0 {
		return nil, errors.New("invalid zero exponent")
	}

	return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: e}, nil
}
