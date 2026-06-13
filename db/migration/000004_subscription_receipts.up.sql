-- StoreKit 2 subscription receipts: one row per original transaction, kept in
-- sync by /subscription/verify and the App Store Server Notifications webhook.

CREATE TYPE subscription_environment AS ENUM ('sandbox', 'production');
CREATE TYPE receipt_status AS ENUM ('active', 'expired', 'revoked', 'billing_retry');

CREATE TABLE subscription_receipts (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    original_tx_id text NOT NULL UNIQUE,                 -- StoreKit 2 originalTransactionId
    product_id     text NOT NULL,
    environment    subscription_environment NOT NULL,
    status         receipt_status NOT NULL,
    expires_at     timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX subscription_receipts_user_idx ON subscription_receipts (user_id);
