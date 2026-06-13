package token

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Errors returned when a token fails verification.
var (
	ErrExpiredToken = errors.New("token has expired")
	ErrInvalidToken = errors.New("token is invalid")
)

// Payload is the data carried inside a token. It implements jwt.Claims (v5).
type Payload struct {
	ID        uuid.UUID `json:"id"`
	UserID    uuid.UUID `json:"user_id"`
	Email     string    `json:"email"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiredAt time.Time `json:"expired_at"`
}

// NewPayload builds a payload for a user valid for the given duration.
func NewPayload(userID uuid.UUID, email string, duration time.Duration) (*Payload, error) {
	tokenID, err := uuid.NewRandom()
	if err != nil {
		return nil, err
	}

	now := time.Now()
	return &Payload{
		ID:        tokenID,
		UserID:    userID,
		Email:     email,
		IssuedAt:  now,
		ExpiredAt: now.Add(duration),
	}, nil
}

// jwt.Claims (v5) implementation.

func (p *Payload) GetExpirationTime() (*jwt.NumericDate, error) {
	return jwt.NewNumericDate(p.ExpiredAt), nil
}

func (p *Payload) GetIssuedAt() (*jwt.NumericDate, error) {
	return jwt.NewNumericDate(p.IssuedAt), nil
}

func (p *Payload) GetNotBefore() (*jwt.NumericDate, error) {
	return jwt.NewNumericDate(p.IssuedAt), nil
}

func (p *Payload) GetIssuer() (string, error) { return "glutenfree", nil }

func (p *Payload) GetSubject() (string, error) { return p.UserID.String(), nil }

func (p *Payload) GetAudience() (jwt.ClaimStrings, error) { return nil, nil }
