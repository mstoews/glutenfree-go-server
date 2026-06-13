package token

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const minSecretKeySize = 32

// JWTMaker is a Maker backed by symmetric (HS256) JSON Web Tokens.
type JWTMaker struct {
	secretKey string
}

// NewJWTMaker creates a JWTMaker. The secret key must be at least 32 bytes.
func NewJWTMaker(secretKey string) (*JWTMaker, error) {
	if len(secretKey) < minSecretKeySize {
		return nil, fmt.Errorf("invalid key size: must be at least %d characters", minSecretKeySize)
	}
	return &JWTMaker{secretKey}, nil
}

// CreateToken signs an app-user (RoleUser) token valid for the given duration.
func (maker *JWTMaker) CreateToken(userID uuid.UUID, email string, duration time.Duration) (string, *Payload, error) {
	return maker.CreateRoleToken(userID, email, RoleUser, nil, duration)
}

// CreateRoleToken signs a token with an explicit role and optional store scope.
func (maker *JWTMaker) CreateRoleToken(userID uuid.UUID, email, role string, storeID *uuid.UUID, duration time.Duration) (string, *Payload, error) {
	payload, err := NewPayload(userID, email, role, storeID, duration)
	if err != nil {
		return "", payload, err
	}

	jwtToken := jwt.NewWithClaims(jwt.SigningMethodHS256, payload)
	signed, err := jwtToken.SignedString([]byte(maker.secretKey))
	return signed, payload, err
}

// VerifyToken validates a token and returns its payload.
func (maker *JWTMaker) VerifyToken(token string) (*Payload, error) {
	keyFunc := func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return []byte(maker.secretKey), nil
	}

	parsed, err := jwt.ParseWithClaims(token, &Payload{}, keyFunc)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrExpiredToken
		}
		return nil, ErrInvalidToken
	}

	payload, ok := parsed.Claims.(*Payload)
	if !ok {
		return nil, ErrInvalidToken
	}
	return payload, nil
}
