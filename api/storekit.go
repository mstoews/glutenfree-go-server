package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mstoews/glutenfree-server/appstore"
	db "github.com/mstoews/glutenfree-server/db/sqlc"
	"github.com/mstoews/glutenfree-server/token"
	"github.com/rs/zerolog/log"
)

var errStoreKitNotConfigured = errors.New("apple subscription verification is not configured")

type verifySubscriptionRequest struct {
	SignedTransaction string `json:"signedTransaction" binding:"required"`
}

type subscriptionResponse struct {
	SubscriptionStatus string     `json:"subscription_status"`
	SubExpiresAt       *time.Time `json:"sub_expires_at"`
	OriginalTxID       string     `json:"original_tx_id"`
	Environment        string     `json:"environment"`
}

// verifySubscription verifies a StoreKit 2 signed transaction (JWS), records a
// receipt, and updates the authenticated user's subscription status.
func (server *Server) verifySubscription(ctx *gin.Context) {
	if server.appstore == nil {
		ctx.JSON(http.StatusNotImplemented, errorResponse(errStoreKitNotConfigured))
		return
	}

	authPayload := ctx.MustGet(authorizationPayloadKey).(*token.Payload)

	var req verifySubscriptionRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(err))
		return
	}

	tx, err := server.appstore.VerifyTransaction(req.SignedTransaction)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(fmt.Errorf("invalid signed transaction: %w", err)))
		return
	}
	if tx.OriginalTransactionID == "" {
		ctx.JSON(http.StatusBadRequest, errorResponse(errors.New("transaction missing originalTransactionId")))
		return
	}

	receiptStatus, userStatus := deriveStatus(tx, time.Now())
	expires := expiryTimestamptz(tx.ExpiresDateMS)

	receipt, err := server.store.UpsertSubscriptionReceipt(ctx, db.UpsertSubscriptionReceiptParams{
		UserID:       authPayload.UserID,
		OriginalTxID: tx.OriginalTransactionID,
		ProductID:    tx.ProductID,
		Environment:  environmentFromApple(tx.Environment),
		Status:       receiptStatus,
		ExpiresAt:    expires,
	})
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	user, err := server.store.UpdateSubscription(ctx, db.UpdateSubscriptionParams{
		ID:                 authPayload.UserID,
		SubscriptionStatus: userStatus,
		SubExpiresAt:       expires,
	})
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	resp := subscriptionResponse{
		SubscriptionStatus: string(user.SubscriptionStatus),
		OriginalTxID:       receipt.OriginalTxID,
		Environment:        string(receipt.Environment),
	}
	if user.SubExpiresAt.Valid {
		t := user.SubExpiresAt.Time
		resp.SubExpiresAt = &t
	}
	ctx.JSON(http.StatusOK, resp)
}

type appleWebhookRequest struct {
	SignedPayload string `json:"signedPayload" binding:"required"`
}

// appleWebhook handles App Store Server Notifications V2. It verifies the
// notification, maps it to a local receipt by original transaction id, and
// updates the linked user. Returns 200 even for unmappable notifications so
// Apple does not retry indefinitely.
func (server *Server) appleWebhook(ctx *gin.Context) {
	if server.appstore == nil {
		ctx.JSON(http.StatusNotImplemented, errorResponse(errStoreKitNotConfigured))
		return
	}

	var req appleWebhookRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(err))
		return
	}

	notif, tx, err := server.appstore.VerifyNotification(req.SignedPayload)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, errorResponse(fmt.Errorf("invalid notification: %w", err)))
		return
	}
	if tx == nil || tx.OriginalTransactionID == "" {
		log.Warn().Str("type", notif.NotificationType).Msg("apple notification without transaction; acknowledged")
		ctx.JSON(http.StatusOK, gin.H{"ok": true, "handled": false})
		return
	}

	receipt, err := server.store.GetReceiptByOriginalTxID(ctx, tx.OriginalTransactionID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// No local user linked yet (e.g. webhook before first verify). Ack.
			log.Warn().
				Str("original_tx_id", tx.OriginalTransactionID).
				Str("type", notif.NotificationType).
				Msg("apple notification for unknown receipt; acknowledged")
			ctx.JSON(http.StatusOK, gin.H{"ok": true, "handled": false})
			return
		}
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	receiptStatus, userStatus := statusFromNotification(notif.NotificationType, tx, time.Now())
	expires := expiryTimestamptz(tx.ExpiresDateMS)

	if _, err := server.store.UpsertSubscriptionReceipt(ctx, db.UpsertSubscriptionReceiptParams{
		UserID:       receipt.UserID,
		OriginalTxID: tx.OriginalTransactionID,
		ProductID:    tx.ProductID,
		Environment:  environmentFromApple(tx.Environment),
		Status:       receiptStatus,
		ExpiresAt:    expires,
	}); err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}
	if _, err := server.store.UpdateSubscription(ctx, db.UpdateSubscriptionParams{
		ID:                 receipt.UserID,
		SubscriptionStatus: userStatus,
		SubExpiresAt:       expires,
	}); err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	log.Info().
		Str("type", notif.NotificationType).
		Str("user_status", string(userStatus)).
		Msg("apple notification handled")
	ctx.JSON(http.StatusOK, gin.H{"ok": true, "handled": true})
}

// deriveStatus computes receipt + user status from a transaction's own fields.
func deriveStatus(tx *appstore.TransactionPayload, now time.Time) (db.ReceiptStatus, db.SubscriptionStatus) {
	switch {
	case tx.IsRevoked():
		return db.ReceiptStatusRevoked, db.SubscriptionStatusRevoked
	case tx.ExpiresDateMS > 0 && tx.ExpiresAt().Before(now):
		return db.ReceiptStatusExpired, db.SubscriptionStatusExpired
	default:
		return db.ReceiptStatusActive, db.SubscriptionStatusActive
	}
}

// statusFromNotification maps a notification type to receipt + user status,
// falling back to the transaction-derived status.
func statusFromNotification(nType string, tx *appstore.TransactionPayload, now time.Time) (db.ReceiptStatus, db.SubscriptionStatus) {
	switch nType {
	case "SUBSCRIBED", "DID_RENEW":
		return db.ReceiptStatusActive, db.SubscriptionStatusActive
	case "DID_FAIL_TO_RENEW":
		// Billing retry / grace: the user keeps access until the sub actually
		// expires (an EXPIRED notification flips them later).
		return db.ReceiptStatusBillingRetry, db.SubscriptionStatusActive
	case "EXPIRED":
		return db.ReceiptStatusExpired, db.SubscriptionStatusExpired
	case "REVOKE":
		return db.ReceiptStatusRevoked, db.SubscriptionStatusRevoked
	default:
		return deriveStatus(tx, now)
	}
}

func expiryTimestamptz(ms int64) pgtype.Timestamptz {
	if ms <= 0 {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: time.UnixMilli(ms), Valid: true}
}

func environmentFromApple(env string) db.SubscriptionEnvironment {
	if strings.EqualFold(env, "Production") {
		return db.SubscriptionEnvironmentProduction
	}
	return db.SubscriptionEnvironmentSandbox
}
