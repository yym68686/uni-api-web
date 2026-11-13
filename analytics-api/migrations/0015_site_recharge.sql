-- Additive and opt-in: no existing account or probe is changed.
CREATE TABLE console_recharge_settings (
 account_id text PRIMARY KEY REFERENCES console_sub_accounts(id) ON DELETE CASCADE,
 revision text NOT NULL,
 config jsonb NOT NULL,
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE console_recharge_quotes (
 id text PRIMARY KEY,
 owner text NOT NULL,
 account_id text NOT NULL REFERENCES console_sub_accounts(id) ON DELETE CASCADE,
 revision text NOT NULL,
 public jsonb NOT NULL,
 encrypted_context text NOT NULL,
 expires_at timestamptz NOT NULL
);
CREATE INDEX console_recharge_quotes_owner ON console_recharge_quotes(owner, account_id);
CREATE TABLE console_recharge_jobs (
 id text PRIMARY KEY,
 owner text NOT NULL,
 account_id text NOT NULL REFERENCES console_sub_accounts(id) ON DELETE RESTRICT,
 quote_id text NOT NULL UNIQUE,
 idempotency_key text NOT NULL,
 state text NOT NULL,
 public jsonb NOT NULL,
 encrypted_context text NOT NULL,
 message text NOT NULL DEFAULT '',
 lease_token text NOT NULL DEFAULT '',
 lease_until timestamptz,
 next_attempt timestamptz NOT NULL DEFAULT now(),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(owner, idempotency_key)
);
-- Unknown payment/redemption outcomes remain active and block another purchase.
CREATE UNIQUE INDEX console_recharge_one_active ON console_recharge_jobs(account_id)
 WHERE state NOT IN ('done','closed','failed');
CREATE INDEX console_recharge_work ON console_recharge_jobs(next_attempt)
 WHERE state NOT IN ('done','closed','failed','review');
CREATE TABLE console_recharge_wallet_locks (
 owner text PRIMARY KEY,
 token text NOT NULL,
 expires_at timestamptz NOT NULL
);
CREATE TABLE console_recharge_events (
 id bigserial PRIMARY KEY,
 job_id text NOT NULL REFERENCES console_recharge_jobs(id) ON DELETE CASCADE,
 state text NOT NULL,
 message text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
