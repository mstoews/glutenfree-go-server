// Package appstore verifies Apple StoreKit 2 signed transactions and App Store
// Server Notifications (V2) locally: it checks the JWS x5c certificate chain
// against trusted Apple roots and verifies the ES256 signature, without making
// a per-transaction App Store API call.
package appstore

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TransactionPayload is the decoded JWSTransactionDecodedPayload. RegisteredClaims
// is embedded only to satisfy jwt.Claims; Apple populates the fields below.
type TransactionPayload struct {
	jwt.RegisteredClaims
	TransactionID          string `json:"transactionId"`
	OriginalTransactionID  string `json:"originalTransactionId"`
	BundleID               string `json:"bundleId"`
	ProductID              string `json:"productId"`
	SubscriptionGroupID    string `json:"subscriptionGroupIdentifier"`
	PurchaseDateMS         int64  `json:"purchaseDate"`
	OriginalPurchaseDateMS int64  `json:"originalPurchaseDate"`
	ExpiresDateMS          int64  `json:"expiresDate"`
	Type                   string `json:"type"`
	InAppOwnershipType     string `json:"inAppOwnershipType"`
	Environment            string `json:"environment"` // "Sandbox" | "Production"
	RevocationDateMS       int64  `json:"revocationDate"`
	RevocationReason       *int   `json:"revocationReason"`
}

// ExpiresAt returns the subscription expiry, or the zero time if unset.
func (p *TransactionPayload) ExpiresAt() time.Time { return msToTime(p.ExpiresDateMS) }

// IsRevoked reports whether Apple has revoked this transaction.
func (p *TransactionPayload) IsRevoked() bool { return p.RevocationDateMS > 0 }

// NotificationPayload is the decoded App Store Server Notification V2 payload
// (responseBodyV2DecodedPayload).
type NotificationPayload struct {
	jwt.RegisteredClaims
	NotificationType string           `json:"notificationType"`
	Subtype          string           `json:"subtype"`
	NotificationUUID string           `json:"notificationUUID"`
	Version          string           `json:"version"`
	SignedDateMS     int64            `json:"signedDate"`
	Data             NotificationData `json:"data"`
}

// NotificationData carries the nested signed JWS blobs for the affected sub.
type NotificationData struct {
	AppAppleID            int64  `json:"appAppleId"`
	BundleID              string `json:"bundleId"`
	BundleVersion         string `json:"bundleVersion"`
	Environment           string `json:"environment"`
	SignedTransactionInfo string `json:"signedTransactionInfo"`
	SignedRenewalInfo     string `json:"signedRenewalInfo"`
}

func msToTime(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}
