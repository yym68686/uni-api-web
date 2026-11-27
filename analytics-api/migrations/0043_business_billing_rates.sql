-- Keep one recent verified sample per exact billing identity. Do not backfill
-- or rewrite the spend ledger: old rows have no verifiable multiplier.
CREATE TABLE console_sub_business_rates (
 account_id text NOT NULL REFERENCES console_sub_accounts(id) ON DELETE CASCADE,
 group_id bigint NOT NULL,
 key_id bigint NOT NULL,
 model text NOT NULL,
 at_ms bigint NOT NULL,
 log_id bigint NOT NULL,
 rate double precision NOT NULL CHECK (rate >= 0 AND rate < 'Infinity'::double precision),
 PRIMARY KEY (account_id, group_id, key_id, model)
);
