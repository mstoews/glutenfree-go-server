package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mstoews/glutenfree-server/applesignin"
	db "github.com/mstoews/glutenfree-server/db/sqlc"
)

type appleSignInRequest struct {
	// IdentityToken is the JWT from ASAuthorizationAppleIDCredential.identityToken.
	IdentityToken string `json:"identity_token" binding:"required"`
	// Email is forwarded by the client from the FIRST authorization only (Apple
	// omits it on subsequent logins). Used when the token carries no email claim.
	Email string `json:"email"`
}

// appleSignIn verifies an Apple identity token and logs the user in, creating
// an Apple-only account on first sign-in. Returns the standard login response.
func (server *Server) appleSignIn(ctx *gin.Context) {
	if server.apple == nil {
		ctx.JSON(http.StatusNotImplemented,
			errorResponse(errors.New("apple sign-in is not configured")))
		return
	}

	var req appleSignInRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(err))
		return
	}

	identity, err := server.apple.Verify(ctx, req.IdentityToken)
	if err != nil {
		ctx.JSON(http.StatusUnauthorized,
			errorResponse(errors.New("invalid apple identity token")))
		return
	}

	appleID := pgtype.Text{String: identity.Subject, Valid: true}
	user, err := server.store.GetUserByAppleID(ctx, appleID)
	if errors.Is(err, pgx.ErrNoRows) {
		user, err = server.createAppleUser(ctx, identity, req.Email, appleID)
	}
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			ctx.JSON(http.StatusConflict, errorResponse(
				errors.New("an account with this email already exists; sign in with email and password")))
			return
		}
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	server.issueSession(ctx, user)
}

// createAppleUser provisions a new Apple-only account (no password hash).
func (server *Server) createAppleUser(
	ctx *gin.Context, identity applesignin.Identity, fallbackEmail string, appleID pgtype.Text,
) (db.User, error) {
	email := firstNonEmpty(identity.Email, fallbackEmail)
	if email == "" {
		// "Hide My Email" relay wasn't forwarded and the token had no email —
		// synthesize a stable, unique placeholder to satisfy the NOT NULL column.
		email = identity.Subject + "@privaterelay.appleid.local"
	}

	return server.store.CreateUser(ctx, db.CreateUserParams{
		Email:        email,
		PasswordHash: pgtype.Text{}, // Apple-only account: no password
		AppleUserID:  appleID,
	})
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
