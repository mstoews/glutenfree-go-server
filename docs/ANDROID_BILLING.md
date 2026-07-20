# Android / Google Play Billing — design sketch

**Status: sketch only. Nothing here is implemented.** Written to size the work
before committing to it.

Goal: let non-iOS users pay, reusing the existing subscription model
(`users.subscription_status` gates `GET /stores/:id/menu` with a 402).

---

## 1. The key architectural difference

`playbilling` is **not** a mirror of `appstore`. The two platforms verify
purchases in fundamentally different ways:

| | Apple (StoreKit 2) — implemented | Google Play — proposed |
|---|---|---|
| Client sends | signed JWS transaction | **opaque purchase token** |
| Verification | **offline** — JWS signature vs Apple Root CA | **online** — call Play Developer API with a service-account token |
| Stable identity | `originalTransactionId`, stable forever | **purchase token changes** on upgrade/downgrade/resubscribe |
| Notifications | App Store Server Notifications V2 → signed JWS POSTed to us | **RTDN via Cloud Pub/Sub** → push message, then we call the API for state |
| Credentials | `APPLE_ROOT_CA_PATH` (a cert) | GCP service account JSON + Play Console linkage |
| Ack required | no | **yes — acknowledge within 3 days or Google auto-refunds** |

Consequence: `appstore.Verifier` is self-contained crypto. `playbilling` is an
API client with an outbound dependency (latency, quota, retries, credentials).
Different failure modes, similar amount of code.

---

## 2. Schema change

`subscription_receipts` is ~80% provider-agnostic already. It needs a provider
discriminator, and the unique key must be scoped per provider.

```sql
-- 000011_subscription_provider.up.sql
CREATE TYPE subscription_provider AS ENUM ('apple', 'google');

ALTER TABLE subscription_receipts
    ADD COLUMN provider subscription_provider NOT NULL DEFAULT 'apple';

-- original_tx_id is only unique *within* a provider.
ALTER TABLE subscription_receipts
    DROP CONSTRAINT subscription_receipts_original_tx_id_key;
CREATE UNIQUE INDEX subscription_receipts_provider_tx_key
    ON subscription_receipts (provider, original_tx_id);

-- Play chains an old token to its replacement on upgrade/resubscribe; keep the
-- link so a renewed token can supersede the previous row rather than duplicate it.
ALTER TABLE subscription_receipts
    ADD COLUMN linked_tx_id text;
```

Migration risk: **none today — the table has 0 rows.** The `DEFAULT 'apple'`
keeps existing (and any in-flight) Apple rows correct.

`original_tx_id` keeps its name but widens in meaning: "the provider's purchase
identity" — Apple's `originalTransactionId`, or Play's purchase token.

Query changes (`db/query/subscription_receipts.sql`):
- `UpsertSubscriptionReceipt` → add `provider`, `ON CONFLICT (provider, original_tx_id)`.
- `GetReceiptByOriginalTxID` → scope by provider (or it can collide across providers).

Optional, for Google Sign-In parity with Apple:
```sql
ALTER TABLE users ADD COLUMN google_user_id text;
CREATE UNIQUE INDEX users_google_user_id_key ON users (google_user_id)
    WHERE google_user_id IS NOT NULL;
```

---

## 3. `playbilling` package

Mirrors the shape of `appstore` so `api/` code reads the same, but backed by an
HTTP client rather than certificate validation.

```go
package playbilling

// Verifier calls the Play Developer API with a service account.
type Verifier struct {
    client      *http.Client // oauth2 service-account client, androidpublisher scope
    packageName string
}

func NewVerifier(ctx context.Context, saJSON []byte, packageName string) (*Verifier, error)

// SubscriptionPurchase is the subset of purchases.subscriptionsv2 we need —
// the analogue of appstore.TransactionPayload.
type SubscriptionPurchase struct {
    PurchaseToken       string
    LinkedPurchaseToken string    // set when this token supersedes an older one
    ProductID           string
    ExpiryTime          time.Time
    State               string    // ACTIVE | CANCELED | IN_GRACE_PERIOD | ON_HOLD | EXPIRED
    AcknowledgementState string   // ACKNOWLEDGED | PENDING
    IsTestPurchase      bool      // -> maps onto the existing sandbox/production enum
    ExternalAccountID   string    // obfuscatedExternalAccountId == our user_id (set at purchase)
}

// Verify fetches authoritative state for a purchase token.
func (v *Verifier) Verify(ctx context.Context, purchaseToken string) (*SubscriptionPurchase, error)

// Acknowledge must be called within 3 days or Google refunds the purchase.
func (v *Verifier) Acknowledge(ctx context.Context, purchaseToken string) error
```

Status mapping mirrors `deriveStatus`/`statusFromNotification` in `storekit.go`:

| Play state | receipt_status | subscription_status |
|---|---|---|
| `ACTIVE` | active | active |
| `IN_GRACE_PERIOD` | billing_retry | **active** (matches the Apple `DID_FAIL_TO_RENEW` behaviour) |
| `ON_HOLD` / `PAUSED` | billing_retry | expired |
| `CANCELED` (still within expiry) | active | active |
| `EXPIRED` | expired | expired |
| refund/revoke (`VOIDED` RTDN) | revoked | revoked |

The existing `subscription_environment` enum is reusable:
`IsTestPurchase → sandbox`, else `production`.

---

## 4. API surface

```go
// authed
POST /subscription/verify/google   {"purchaseToken": "..."}   -> subscriptionResponse
// public (Pub/Sub push target)
POST /webhooks/google              RTDN envelope
```

`api/playbilling.go` mirrors `api/storekit.go`:
- `verifyGoogleSubscription` — Verify → **Acknowledge if pending** → upsert receipt → `UpdateSubscription`.
- `googleWebhook` — decode the Pub/Sub envelope (`{message:{data:<base64 JSON>}}`),
  pull `subscriptionNotification.purchaseToken`, **call Verify for authoritative
  state** (the notification itself is not trustworthy state), then upsert + update.
  Return 200 even when unmappable, so Pub/Sub stops retrying — same policy as
  `appleWebhook`.

`Server` gains `playbilling *playbilling.Verifier`, nil when unconfigured → 501,
exactly like `server.appstore`.

Config: `GOOGLE_PLAY_SA_JSON` (Secret Manager) + `ANDROID_PACKAGE_NAME`.

---

## 5. Gotchas that will cost you time

1. **Acknowledgement deadline.** Play auto-refunds any purchase not acknowledged
   within 3 days. Apple has no equivalent. Easy to miss; silently costs revenue.
2. **Purchase tokens aren't stable.** Upgrade/downgrade/resubscribe mints a new
   token with `linkedPurchaseToken` pointing at the old one. Without chaining you
   get duplicate receipt rows per user. Set `obfuscatedExternalAccountId` to the
   user id at purchase time so you can always map back.
3. **RTDN is Pub/Sub, not a webhook.** Needs a topic + push subscription aimed at
   the Cloud Run URL, and the push endpoint should verify the OIDC token
   (otherwise anyone can POST it). Pub/Sub also redelivers — handlers must be
   idempotent (the upsert already is).
4. **Verification is a network call.** Google API outage/quota = can't verify.
   Needs timeouts + retry, and a decision on fail-open vs fail-closed.
5. **Dual-provider entitlement — the real design question.**
   `users.subscription_status` is single-valued and every writer overwrites it
   (last-write-wins). A user with both an Apple and a Google subscription (or who
   switches platforms) will flap. The fix is to stop treating it as a writable
   field and derive it: *active if any non-revoked receipt is active*. That's a
   change to the existing Apple path too, not just new code.

---

## 6. Sizing

| Piece | Size |
|---|---|
| Migration + query changes | ~0.5 day |
| `playbilling` package (auth, verify, acknowledge, mapping, tests) | 2–3 days |
| `api/playbilling.go` (verify endpoint + RTDN webhook) | 1–2 days |
| GCP wiring (service account, Play Console link, Pub/Sub topic/subscription, Secret Manager) | ~0.5 day, mostly console + IAM |
| Derived entitlement rework (gotcha #5) | 1 day |
| Google Sign-In (`google_user_id` + `/auth/google`) — optional | ~1 day |
| **Backend total** | **~1–1.5 weeks** |
| **Android client** (new app, Billing integration, UI parity, store review) | **weeks–months** |

**The backend is not the expensive part.** The client is. The server work is
well-understood because the Apple path already established the pattern; the
Android app is a whole new product surface to build and then maintain forever.

---

## 7. Recommendation

Sequence unchanged: the catalog is the bottleneck (8 approved vs 113 drafts), and
the public web directory is days of work for pure acquisition. Android is the
right *monetization* vehicle for non-iOS users — just size it as a client
project with a modest backend tail, not a backend project.

Before committing: check analytics for the iOS/Android split of your actual
audience. Japan skews unusually iOS-heavy, but the English-speaking
tourist/expat segment (which the bilingual UI targets) is Android-majority
worldwide. That ratio decides whether this is a 30% or a 70% market.
