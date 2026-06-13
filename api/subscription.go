package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/mstoews/glutenfree-server/token"
)

type subscriptionStatusResponse struct {
	SubscriptionStatus string     `json:"subscription_status"`
	IsActive           bool       `json:"is_active"`
	SubExpiresAt       *time.Time `json:"sub_expires_at"`
}

// getSubscriptionStatus returns the authenticated user's current tier. Content
// gating (paid-only menus) keys off this; for now it reflects the users row.
func (server *Server) getSubscriptionStatus(ctx *gin.Context) {
	authPayload := ctx.MustGet(authorizationPayloadKey).(*token.Payload)

	user, err := server.store.GetUserByID(ctx, authPayload.UserID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			ctx.JSON(http.StatusNotFound, errorResponse(errors.New("user not found")))
			return
		}
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	resp := subscriptionStatusResponse{
		SubscriptionStatus: string(user.SubscriptionStatus),
		IsActive:           string(user.SubscriptionStatus) == "active",
	}
	if user.SubExpiresAt.Valid {
		t := user.SubExpiresAt.Time
		resp.SubExpiresAt = &t
	}
	ctx.JSON(http.StatusOK, resp)
}
