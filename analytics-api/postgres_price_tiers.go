package main

import (
	"context"
	"log"
	"strings"
	"time"
)

const priceTierDimensions = "source_id,kind,provider,model,upstream_model,endpoint,stream,outcome,key_id"
const priceTierValues = "ordinary_tokens,output_tokens,cache_read_tokens,cache_write_tokens,cache_write_1h_tokens"

// Admission already enforces a separate reserve for the main history. These
// auxiliary writes are capped; demanding another 64 MiB made them inert on a
// healthy steady-state database running near its retention watermark.
const spendProjectionReserve = 16 << 20
const spendProjectionDiskLimit = 16 << 20

// Hourly rollups retain model-independent token amounts for both supported
// thresholds. An hour is readable only after its retained history was seeded.
// Readers use the original usage path for uncovered hours and partial edges.
const priceTierSchema = `
 CREATE TABLE IF NOT EXISTS price_tier_hours(period_ms bigint PRIMARY KEY);
 CREATE TABLE IF NOT EXISTS price_tier_rollups(
   period_ms bigint NOT NULL REFERENCES price_tier_hours(period_ms) ON DELETE CASCADE,
   threshold bigint NOT NULL, source_id text NOT NULL, kind text NOT NULL,
   provider text NOT NULL, model text NOT NULL, upstream_model text NOT NULL,
   endpoint text NOT NULL, stream boolean NOT NULL, outcome text NOT NULL, key_id text NOT NULL,
   ordinary_tokens bigint NOT NULL, output_tokens bigint NOT NULL, cache_read_tokens bigint NOT NULL,
   cache_write_tokens bigint NOT NULL, cache_write_1h_tokens bigint NOT NULL,
   PRIMARY KEY(period_ms,threshold,source_id,kind,provider,model,upstream_model,endpoint,stream,outcome,key_id));`

func priceTierInsert(source, condition string) string {
	keys := "period_ms,threshold," + priceTierDimensions
	updates := make([]string, 0, 5)
	for _, name := range strings.Split(priceTierValues, ",") {
		updates = append(updates, name+"=price_tier_rollups."+name+"+excluded."+name)
	}
	return `INSERT INTO price_tier_rollups(` + keys + `,` + priceTierValues + `)
 SELECT u.at_ms/3600000*3600000 AS period_ms,t.threshold,u.source_id,u.kind,u.provider,u.model,u.upstream_model,
        u.endpoint,u.stream,u.outcome,coalesce(u.key_id,'') AS key_id,
        sum(greatest(0,u.input_tokens-coalesce(u.cache_read_tokens,0)-coalesce(u.cache_write_tokens,0)))::bigint,
        sum(coalesce(u.output_tokens,0))::bigint,sum(coalesce(u.cache_read_tokens,0))::bigint,
        sum(coalesce(u.cache_write_tokens,0))::bigint,sum(coalesce(u.cache_write_1h_tokens,0))::bigint
 FROM ` + source + ` u CROSS JOIN (VALUES (100000::bigint),(272000::bigint)) t(threshold)
 WHERE u.kind NOT IN ('billing','trace') AND u.input_tokens>100000 AND u.input_tokens>t.threshold
 -- The exact tier query joins these fields by equality to NOT NULL base rollups.
 AND u.provider IS NOT NULL AND u.model IS NOT NULL AND u.upstream_model IS NOT NULL
 AND u.endpoint IS NOT NULL AND u.stream IS NOT NULL AND u.outcome IS NOT NULL AND (` + condition + `)
 GROUP BY 1,2,3,4,5,6,7,8,9,10,11 ON CONFLICT(` + keys + `) DO UPDATE SET ` + strings.Join(updates, ",")
}

func (e *Engine) initializePriceTierRollups(ctx context.Context) error {
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(8395014)"); err != nil {
		return err
	}
	var installed bool
	if err = tx.QueryRowContext(ctx, "SELECT to_regclass('price_tier_hours') IS NOT NULL").Scan(&installed); err != nil {
		return err
	}
	if !installed {
		ddl := priceTierSchema + `
 CREATE FUNCTION price_tier_write_v1() RETURNS TRIGGER LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
 BEGIN
 ` + priceTierInsert("new_tier_facts", "EXISTS(SELECT 1 FROM price_tier_hours h WHERE h.period_ms=u.at_ms/3600000*3600000)") + `;
 RETURN NULL;
 END $$;
 CREATE TRIGGER price_tier_write_v1 AFTER INSERT ON facts REFERENCING NEW TABLE AS new_tier_facts
 FOR EACH STATEMENT EXECUTE FUNCTION price_tier_write_v1();
 CREATE FUNCTION price_tier_expire_v1() RETURNS TRIGGER LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
 BEGIN DELETE FROM price_tier_hours WHERE period_ms=OLD.start_ms; RETURN NULL; END $$;
 CREATE TRIGGER price_tier_expire_v1 AFTER DELETE ON fact_partitions FOR EACH ROW EXECUTE FUNCTION price_tier_expire_v1();`
		if _, err = tx.ExecContext(ctx, ddl); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Seed one newest retained hour under the same admission lock as writes and
// FIFO. Marking it covered and reading its baseline are one transaction. Old
// replicas also execute the facts trigger during rolling deploys and rollback.
func (e *Engine) seedPriceTierHour(ctx context.Context) (bool, error) {
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var locked bool
	if err = tx.QueryRowContext(ctx, "SELECT pg_try_advisory_xact_lock(8395015)").Scan(&locked); err != nil || !locked {
		return false, err
	}
	var hour int64
	err = tx.QueryRowContext(ctx, `SELECT coalesce((SELECT p.start_ms FROM fact_partitions p
 WHERE NOT EXISTS(SELECT 1 FROM price_tier_hours h WHERE h.period_ms=p.start_ms)
 AND EXISTS(SELECT 1 FROM meta WHERE name='usage_projection_complete' AND value='true')
 ORDER BY p.start_ms DESC LIMIT 1),-1)`).Scan(&hour)
	if err != nil {
		return false, err
	}
	if hour < 0 {
		return false, nil
	}
	var room bool
	if err = tx.QueryRowContext(ctx, `SELECT pg_database_size(current_database())+? < ingest_limit_bytes
 AND pg_total_relation_size('price_tier_rollups')<? FROM storage_state WHERE singleton`, spendProjectionReserve, spendProjectionDiskLimit).Scan(&room); err != nil {
		return false, err
	}
	if !room {
		return false, errSpendProjectionCapacity
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO price_tier_hours(period_ms) VALUES(?)", hour); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, priceTierInsert("usage_facts", "u.at_ms>=? AND u.at_ms<?"), hour, hour+3600000); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (s *Service) priceTierLoop(ctx context.Context) {
	var lastDeferred time.Time
	for ctx.Err() == nil {
		// Never let an unexpectedly large hour hold ingestion for long. Until
		// backfill succeeds that hour stays on the unchanged reader.
		call, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
		ok, err := s.engine.seedPriceTierHour(call)
		cancel()
		delay := 15 * time.Second
		if ok && err == nil {
			delay = time.Second
		} else if err != nil && ctx.Err() == nil && time.Since(lastDeferred) > time.Minute {
			lastDeferred = time.Now()
			log.Printf("event=price_tier_backfill_deferred class=%s", spendProjectionErrorClass(err))
		}
		if !waitStartup(ctx, delay) {
			return
		}
	}
}
