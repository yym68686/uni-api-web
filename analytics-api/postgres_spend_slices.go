package main

import "context"

const spendIdentityDigestSQL = `encode(sha256(convert_to(jsonb_build_array(source_id,instance_id,request_id,attempt_id,provider,key_id,model,upstream_model,endpoint,stream)::text,'UTF8')),'hex')`

const spendSliceSchema = `
 CREATE TABLE channel_spend_epoch(singleton boolean PRIMARY KEY CHECK(singleton),epoch bigint NOT NULL DEFAULT 0);
 INSERT INTO channel_spend_epoch(singleton) VALUES(true);
 CREATE TABLE channel_spend_slices(
   scope text NOT NULL, from_sec bigint NOT NULL, to_sec bigint NOT NULL, binding text NOT NULL,
   payload jsonb NOT NULL, identities text[] NOT NULL, claims text[] NOT NULL, receipts text[] NOT NULL,
   evidence text[] NOT NULL, size_bytes integer NOT NULL, saved_at timestamptz NOT NULL DEFAULT now(),
   PRIMARY KEY(scope,from_sec,to_sec));
 CREATE INDEX channel_spend_slices_time ON channel_spend_slices(to_sec,from_sec);
 CREATE INDEX channel_spend_slices_identity ON channel_spend_slices USING gin(identities);
 CREATE INDEX channel_spend_slices_claim ON channel_spend_slices USING gin(claims);
 CREATE INDEX channel_spend_slices_receipt ON channel_spend_slices USING gin(receipts);
 CREATE INDEX channel_spend_slices_evidence ON channel_spend_slices USING gin(evidence);

 CREATE FUNCTION channel_spend_invalidate_facts_v1() RETURNS TRIGGER LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
 DECLARE times bigint[]; deps text[]; keys text[];
 BEGIN
   SELECT array_agg(DISTINCT at_ms/1000), array_agg(DISTINCT ` + spendIdentityDigestSQL + `)
     INTO times,deps FROM inserted_spend_facts WHERE kind IN ('attempt','billing');
   IF times IS NULL THEN RETURN NULL; END IF;
   SELECT array_agg(DISTINCT encode(sha256(convert_to(` + billingSiteSQL + `||E'\n'||coalesce(upstream_key_hash,'')||E'\n'||rid,'UTF8')),'hex'))
     INTO keys FROM inserted_spend_facts CROSS JOIN LATERAL unnest(billing_request_ids) AS t(rid) WHERE kind='billing';
   UPDATE channel_spend_epoch SET epoch=epoch+1 WHERE singleton;
   DELETE FROM channel_spend_slices s WHERE s.identities && deps OR s.claims && keys
     OR EXISTS (SELECT 1 FROM unnest(times) AS t(at_sec) WHERE s.from_sec<=at_sec AND at_sec<s.to_sec);
   RETURN NULL;
 END $$;
 CREATE TRIGGER channel_spend_invalidate_facts_v1 AFTER INSERT ON facts REFERENCING NEW TABLE AS inserted_spend_facts
 FOR EACH STATEMENT EXECUTE FUNCTION channel_spend_invalidate_facts_v1();

 CREATE FUNCTION channel_spend_invalidate_claims_v1() RETURNS TRIGGER LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
 DECLARE keys text[];
 BEGIN
   SELECT array_agg(DISTINCT digest) INTO keys FROM touched_spend_claims;
   IF keys IS NULL THEN RETURN NULL; END IF;
   UPDATE channel_spend_epoch SET epoch=epoch+1 WHERE singleton;
   DELETE FROM channel_spend_slices WHERE claims && keys;
   RETURN NULL;
 END $$;
 CREATE TRIGGER channel_spend_claims_insert_v1 AFTER INSERT ON receipt_claim_history REFERENCING NEW TABLE AS touched_spend_claims
 FOR EACH STATEMENT EXECUTE FUNCTION channel_spend_invalidate_claims_v1();
 CREATE TRIGGER channel_spend_claims_update_v1 AFTER UPDATE ON receipt_claim_history REFERENCING NEW TABLE AS touched_spend_claims
 FOR EACH STATEMENT EXECUTE FUNCTION channel_spend_invalidate_claims_v1();

 CREATE FUNCTION channel_spend_invalidate_retention_v1() RETURNS TRIGGER LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
 BEGIN
   IF OLD.evicted_before_ms IS DISTINCT FROM NEW.evicted_before_ms OR OLD.claims_history_complete IS DISTINCT FROM NEW.claims_history_complete THEN
     UPDATE channel_spend_epoch SET epoch=epoch+1 WHERE singleton;
     -- Removing a companion can move a surviving billing fact into another
     -- slice. Rebuild after retention rather than assuming only the oldest is dirty.
     DELETE FROM channel_spend_slices;
   END IF;
   RETURN NULL;
 END $$;
 CREATE TRIGGER channel_spend_retention_v1 AFTER UPDATE OF evicted_before_ms,claims_history_complete ON storage_state
 FOR EACH ROW EXECUTE FUNCTION channel_spend_invalidate_retention_v1();

 CREATE FUNCTION channel_spend_invalidate_receipts_v1(keys text[],events text[]) RETURNS void LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
 BEGIN
   IF coalesce(cardinality(keys),0)+coalesce(cardinality(events),0)=0 THEN RETURN; END IF;
   UPDATE channel_spend_epoch SET epoch=epoch+1 WHERE singleton;
   DELETE FROM channel_spend_slices WHERE receipts && keys OR evidence && events;
 END $$;`

func (e *Engine) initializeSpendSlices(ctx context.Context) error {
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(8395014)"); err != nil {
		return err
	}
	var installed bool
	if err = tx.QueryRowContext(ctx, "SELECT to_regclass('channel_spend_slices') IS NOT NULL").Scan(&installed); err != nil {
		return err
	}
	if !installed {
		if _, err = tx.ExecContext(ctx, spendSliceSchema); err != nil {
			return err
		}
	}
	return tx.Commit()
}
