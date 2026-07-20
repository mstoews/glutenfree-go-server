package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/mstoews/glutenfree-server/db/sqlc"
	"github.com/mstoews/glutenfree-server/mailer"
	"github.com/mstoews/glutenfree-server/token"
	"github.com/mstoews/glutenfree-server/util"
	"github.com/rs/zerolog/log"
)

// Operator-admin account management (/internal/admins) plus the password
// change/reset flows. There is no role hierarchy: every authenticated operator
// may create and reset other operators, matching how the rest of /internal/*
// treats all operators as equally privileged.

const uniqueViolation = "23505" // postgres error code

// ---- responses ----

type internalAdminResponse struct {
	ID        uuid.UUID `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// newInternalAdminResponse projects the row for the API, dropping password_hash
// so a hash can never leak through a handler.
func newInternalAdminResponse(a db.InternalAdmin) internalAdminResponse {
	return internalAdminResponse{
		ID:        a.ID,
		Email:     a.Email,
		Name:      a.Name,
		CreatedAt: a.CreatedAt.Time,
		UpdatedAt: a.UpdatedAt.Time,
	}
}

// authenticatedAdminID returns the operator id carried by the verified access
// token. Safe to call only on routes behind authMiddleware.
func authenticatedAdminID(ctx *gin.Context) uuid.UUID {
	payload := ctx.MustGet(authorizationPayloadKey).(*token.Payload)
	return payload.UserID
}

// ---- list / create ----

func (server *Server) listInternalAdmins(ctx *gin.Context) {
	admins, err := server.store.ListInternalAdmins(ctx)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	out := make([]internalAdminResponse, 0, len(admins))
	for _, a := range admins {
		out = append(out, newInternalAdminResponse(a))
	}
	ctx.JSON(http.StatusOK, out)
}

type createInternalAdminRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=8"`
	Name     string `json:"name"`
}

// createInternalAdmin registers a new operator account. The creating operator
// hands the initial password over out-of-band; the new operator is expected to
// change it via /internal/auth/change-password on first sign-in.
func (server *Server) createInternalAdmin(ctx *gin.Context) {
	var req createInternalAdminRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(err))
		return
	}

	email := strings.TrimSpace(req.Email)

	hash, err := util.HashPassword(req.Password)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	admin, err := server.store.CreateInternalAdmin(ctx, db.CreateInternalAdminParams{
		Email:        email,
		PasswordHash: hash,
		Name:         strings.TrimSpace(req.Name),
	})
	if err != nil {
		// The case-insensitive unique index on email is the authority here;
		// relying on it rather than a pre-check avoids a TOCTOU race between
		// two operators adding the same address.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			ctx.JSON(http.StatusConflict, errorResponse(errors.New("an admin with that email already exists")))
			return
		}
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	log.Info().
		Str("actor_id", authenticatedAdminID(ctx).String()).
		Str("new_admin_id", admin.ID.String()).
		Str("new_admin_email", admin.Email).
		Msg("internal admin created")

	ctx.JSON(http.StatusCreated, newInternalAdminResponse(admin))
}

// ---- password: set for another admin ----

type setInternalAdminPasswordRequest struct {
	Password string `json:"password" binding:"required,min=8"`
}

// setInternalAdminPassword lets one operator reset a colleague's password
// directly — the out-of-band path used when nobody can receive mail. Every
// session belonging to the target is revoked so an attacker holding a stolen
// refresh token loses it.
func (server *Server) setInternalAdminPassword(ctx *gin.Context) {
	id, err := uuid.Parse(ctx.Param("id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(errors.New("invalid admin id")))
		return
	}

	var req setInternalAdminPasswordRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(err))
		return
	}

	if _, err := server.store.GetInternalAdminByID(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			ctx.JSON(http.StatusNotFound, errorResponse(errors.New("admin not found")))
			return
		}
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	if err := server.replaceAdminPassword(ctx, id, req.Password); err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	log.Info().
		Str("actor_id", authenticatedAdminID(ctx).String()).
		Str("target_admin_id", id.String()).
		Msg("internal admin password reset by another admin")

	ctx.JSON(http.StatusOK, gin.H{"password_updated": true})
}

// replaceAdminPassword hashes and stores a new password, then invalidates
// everything minted under the old one: refresh sessions and pending reset
// links. Shared by the admin-initiated, self-service, and emailed reset paths.
func (server *Server) replaceAdminPassword(ctx *gin.Context, adminID uuid.UUID, password string) error {
	hash, err := util.HashPassword(password)
	if err != nil {
		return err
	}
	if _, err := server.store.UpdateInternalAdminPassword(ctx, db.UpdateInternalAdminPasswordParams{
		ID:           adminID,
		PasswordHash: hash,
	}); err != nil {
		return err
	}

	// Best-effort cleanup: the password is already changed, so a failure here
	// must not fail the request. It is logged for follow-up.
	if _, err := server.store.DeleteInternalSessionsForAdmin(ctx, adminID); err != nil {
		log.Error().Err(err).Str("admin_id", adminID.String()).Msg("failed to revoke sessions after password change")
	}
	if _, err := server.store.DeleteInternalPasswordResetsForAdmin(ctx, adminID); err != nil {
		log.Error().Err(err).Str("admin_id", adminID.String()).Msg("failed to clear reset tokens after password change")
	}
	return nil
}

// ---- password: change your own ----

type changeInternalPasswordRequest struct {
	CurrentPassword string `json:"current_password" binding:"required"`
	NewPassword     string `json:"new_password" binding:"required,min=8"`
}

// changeInternalPassword updates the signed-in operator's own password after
// re-checking the current one. Because the change revokes all of this admin's
// sessions (including the caller's), a fresh session is issued and returned so
// the portal can stay signed in.
func (server *Server) changeInternalPassword(ctx *gin.Context) {
	var req changeInternalPasswordRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(err))
		return
	}

	adminID := authenticatedAdminID(ctx)
	admin, err := server.store.GetInternalAdminByID(ctx, adminID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			ctx.JSON(http.StatusNotFound, errorResponse(errors.New("admin not found")))
			return
		}
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	if err := util.CheckPassword(req.CurrentPassword, admin.PasswordHash); err != nil {
		ctx.JSON(http.StatusUnauthorized, errorResponse(errors.New("current password is incorrect")))
		return
	}

	if err := server.replaceAdminPassword(ctx, adminID, req.NewPassword); err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	log.Info().Str("admin_id", adminID.String()).Msg("internal admin changed own password")

	// Writes the new token pair as the response body.
	server.issueInternalSession(ctx, admin)
}

// ---- password: emailed reset ----

// hashResetToken derives the value stored in internal_password_resets. SHA-256
// (not bcrypt) is right here: the token is 256 bits of entropy from crypto/rand,
// so there is nothing to brute-force, and the lookup must be by exact index hit.
func hashResetToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// newResetToken returns a URL-safe 256-bit token.
func newResetToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate reset token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

type forgotInternalPasswordRequest struct {
	Email string `json:"email" binding:"required,email"`
}

// forgotInternalPassword emails a one-time reset link. It always reports
// success for a well-formed request so the endpoint cannot be used to discover
// which addresses have operator accounts.
func (server *Server) forgotInternalPassword(ctx *gin.Context) {
	var req forgotInternalPasswordRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(err))
		return
	}

	// Without a mail provider the caller would wait forever for a mail that is
	// never sent, so say so plainly instead of pretending to have sent it.
	if !server.mailer.Enabled() {
		ctx.JSON(http.StatusNotImplemented, errorResponse(errors.New(
			"email delivery is not configured; ask another operator to reset your password")))
		return
	}
	if server.config.AdminPortalURL == "" {
		log.Error().Msg("ADMIN_PORTAL_URL is unset; cannot build a password reset link")
		ctx.JSON(http.StatusNotImplemented, errorResponse(errors.New("password reset is not configured")))
		return
	}

	// Uniform success response, whatever happened below.
	accepted := gin.H{"sent": true}

	admin, err := server.store.GetInternalAdminByEmail(ctx, strings.TrimSpace(req.Email))
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			log.Error().Err(err).Msg("forgot-password lookup failed")
		}
		ctx.JSON(http.StatusOK, accepted)
		return
	}

	raw, err := newResetToken()
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	expiresAt := time.Now().Add(server.config.PasswordResetTokenDuration)
	if _, err := server.store.CreateInternalPasswordReset(ctx, db.CreateInternalPasswordResetParams{
		AdminID:   admin.ID,
		TokenHash: hashResetToken(raw),
		ExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true},
	}); err != nil {
		log.Error().Err(err).Msg("failed to store password reset token")
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	link := fmt.Sprintf("%s/reset-password?token=%s",
		strings.TrimRight(server.config.AdminPortalURL, "/"), url.QueryEscape(raw))

	if err := server.mailer.Send(ctx, buildResetMail(admin, link, server.config.PasswordResetTokenDuration)); err != nil {
		// Do not surface the provider error: it would confirm the address exists.
		log.Error().Err(err).Str("admin_id", admin.ID.String()).Msg("failed to send password reset email")
	}

	ctx.JSON(http.StatusOK, accepted)
}

// buildResetMail composes the reset message. Kept separate so the copy is easy
// to find and adjust.
func buildResetMail(admin db.InternalAdmin, link string, ttl time.Duration) mailer.Message {
	greeting := "Hello"
	if admin.Name != "" {
		greeting = "Hello " + admin.Name
	}
	validFor := formatDuration(ttl)

	text := fmt.Sprintf(`%s,

Someone requested a password reset for your Gurufuri operator account (%s).

Open this link to choose a new password. It works once and expires in %s:

%s

If you did not request this, you can ignore this email — your password will not change.
`, greeting, admin.Email, validFor, link)

	html := fmt.Sprintf(`<p>%s,</p>
<p>Someone requested a password reset for your Gurufuri operator account (<strong>%s</strong>).</p>
<p><a href="%s">Choose a new password</a></p>
<p>The link works once and expires in %s.</p>
<p>If you did not request this, you can ignore this email &mdash; your password will not change.</p>
`, greeting, admin.Email, link, validFor)

	return mailer.Message{
		To:      admin.Email,
		Subject: "Reset your Gurufuri operator password",
		Text:    text,
		HTML:    html,
	}
}

// formatDuration renders a reset TTL for humans ("1 hour", "30 minutes").
func formatDuration(d time.Duration) string {
	if d >= time.Hour {
		hours := int(d.Hours())
		if hours == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", hours)
	}
	minutes := int(d.Minutes())
	if minutes <= 1 {
		return "1 minute"
	}
	return fmt.Sprintf("%d minutes", minutes)
}

type resetInternalPasswordRequest struct {
	Token       string `json:"token" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=8"`
}

// resetInternalPassword redeems an emailed token and sets a new password.
// Failure modes are deliberately collapsed into one message so a caller cannot
// tell an unknown token from an expired or already-used one.
func (server *Server) resetInternalPassword(ctx *gin.Context) {
	var req resetInternalPasswordRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(err))
		return
	}

	invalid := errors.New("this reset link is invalid or has expired")

	reset, err := server.store.GetInternalPasswordResetByTokenHash(ctx, hashResetToken(req.Token))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			ctx.JSON(http.StatusBadRequest, errorResponse(invalid))
			return
		}
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}
	if reset.UsedAt.Valid || time.Now().After(reset.ExpiresAt.Time) {
		ctx.JSON(http.StatusBadRequest, errorResponse(invalid))
		return
	}

	// Claim the token before changing anything. The query only matches rows
	// still unused, so a second concurrent redemption affects 0 rows and loses.
	claimed, err := server.store.MarkInternalPasswordResetUsed(ctx, reset.ID)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}
	if claimed == 0 {
		ctx.JSON(http.StatusBadRequest, errorResponse(invalid))
		return
	}

	if err := server.replaceAdminPassword(ctx, reset.AdminID, req.NewPassword); err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	log.Info().Str("admin_id", reset.AdminID.String()).Msg("internal admin password reset via email link")

	ctx.JSON(http.StatusOK, gin.H{"password_updated": true})
}
