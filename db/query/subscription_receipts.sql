-- name: UpsertSubscriptionReceipt :one
-- One row per original transaction. Both /subscription/verify and the Apple
-- webhook converge on this so the latest state always wins.
INSERT INTO subscription_receipts (user_id, original_tx_id, product_id, environment, status, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (original_tx_id) DO UPDATE
SET product_id  = EXCLUDED.product_id,
    environment = EXCLUDED.environment,
    status      = EXCLUDED.status,
    expires_at  = EXCLUDED.expires_at,
    updated_at  = now()
RETURNING *;

-- name: GetReceiptByOriginalTxID :one
SELECT * FROM subscription_receipts WHERE original_tx_id = $1;
