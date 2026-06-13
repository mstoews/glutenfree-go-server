package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/mstoews/glutenfree-server/db/sqlc"
	"github.com/mstoews/glutenfree-server/util"
)

var errInvalidCredentials = errors.New("invalid email or password")

type userResponse struct {
	ID                 uuid.UUID  `json:"id"`
	Email              string     `json:"email"`
	SubscriptionStatus string     `json:"subscription_status"`
	SubExpiresAt       *time.Time `json:"sub_expires_at"`
}

func newUserResponse(u db.User) userResponse {
	resp := userResponse{
		ID:                 u.ID,
		Email:              u.Email,
		SubscriptionStatus: string(u.SubscriptionStatus),
	}
	if u.SubExpiresAt.Valid {
		t := u.SubExpiresAt.Time
		resp.SubExpiresAt = &t
	}
	return resp
}

type registerUserRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=8"`
}

func (server *Server) registerUser(ctx *gin.Context) {
	var req registerUserRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(err))
		return
	}

	hashed, err := util.HashPassword(req.Password)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	user, err := server.store.CreateUser(ctx, db.CreateUserParams{
		Email:        req.Email,
		PasswordHash: pgtype.Text{String: hashed, Valid: true},
		AppleUserID:  pgtype.Text{},
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			ctx.JSON(http.StatusConflict, errorResponse(errors.New("email already registered")))
			return
		}
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	ctx.JSON(http.StatusCreated, newUserResponse(user))
}

type loginUserRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

type loginUserResponse struct {
	AccessToken           string       `json:"access_token"`
	AccessTokenExpiresAt  time.Time    `json:"access_token_expires_at"`
	RefreshToken          string       `json:"refresh_token"`
	RefreshTokenExpiresAt time.Time    `json:"refresh_token_expires_at"`
	SessionID             uuid.UUID    `json:"session_id"`
	User                  userResponse `json:"user"`
}

func (server *Server) loginUser(ctx *gin.Context) {
	var req loginUserRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(err))
		return
	}

	user, err := server.store.GetUserByEmail(ctx, req.Email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			ctx.JSON(http.StatusUnauthorized, errorResponse(errInvalidCredentials))
			return
		}
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	// Apple-only accounts have no password hash and cannot log in via password.
	if !user.PasswordHash.Valid {
		ctx.JSON(http.StatusUnauthorized, errorResponse(errInvalidCredentials))
		return
	}
	if err := util.CheckPassword(req.Password, user.PasswordHash.String); err != nil {
		ctx.JSON(http.StatusUnauthorized, errorResponse(errInvalidCredentials))
		return
	}

	accessToken, accessPayload, err := server.tokenMaker.CreateToken(
		user.ID, user.Email, server.config.AccessTokenDuration)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}
	refreshToken, refreshPayload, err := server.tokenMaker.CreateToken(
		user.ID, user.Email, server.config.RefreshTokenDuration)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	session, err := server.store.CreateSession(ctx, db.CreateSessionParams{
		ID:           refreshPayload.ID,
		UserID:       user.ID,
		RefreshToken: refreshToken,
		UserAgent:    ctx.Request.UserAgent(),
		ClientIp:     ctx.ClientIP(),
		IsBlocked:    false,
		ExpiresAt:    pgtype.Timestamptz{Time: refreshPayload.ExpiredAt, Valid: true},
	})
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	ctx.JSON(http.StatusOK, loginUserResponse{
		AccessToken:           accessToken,
		AccessTokenExpiresAt:  accessPayload.ExpiredAt,
		RefreshToken:          refreshToken,
		RefreshTokenExpiresAt: refreshPayload.ExpiredAt,
		SessionID:             session.ID,
		User:                  newUserResponse(user),
	})
}
