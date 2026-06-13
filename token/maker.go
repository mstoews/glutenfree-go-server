package token

import (
	"time"

	"github.com/google/uuid"
)

// Maker is the interface for managing auth tokens.
type Maker interface {
	// CreateToken issues a signed app-user (RoleUser) token valid for the given
	// duration. It returns the token string and the embedded payload.
	CreateToken(userID uuid.UUID, email string, duration time.Duration) (string, *Payload, error)

	// CreateRoleToken issues a token with an explicit role and optional store
	// scope (for store-admin tokens).
	CreateRoleToken(userID uuid.UUID, email, role string, storeID *uuid.UUID, duration time.Duration) (string, *Payload, error)

	// VerifyToken checks whether a token is valid and returns its payload.
	VerifyToken(token string) (*Payload, error)
}
