package main

import (
	"context"
	"fmt"
	"strings"
)

const rollupDimensions = "period_ms,level,kind,source_id,key_id,provider,model,upstream_model,endpoint,stream,outcome"
const rollupSums = "n,input_tokens,output_tokens,cache_read_tokens,cache_write_tokens,cache_write_1h_tokens,usage_samples,cache_samples,actual_cost_usd,actual_cost_samples,first_count,dispatch_count,first_sum,dispatch_sum,created_count,created_sum,text_count,text_sum"

func (e *Engine) rebuildRollup(ctx context.Context, tx *analyticsTx, level string, start, end int64) error {
	if err := ensureRollupPartition(ctx, tx, start); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM rollups WHERE level=? AND period_ms=?", level, start); err != nil {
		return err
	}
	if level == "minute" {
		_, err := tx.ExecContext(ctx, "WITH metrics AS (SELECT facts.*,?::bigint AS period_ms,'minute'::text AS level FROM facts WHERE at_ms>=? AND at_ms<?) INSERT INTO rollups("+rollupColumns()+") "+factRollupSelect("metrics"), start, start, end)
		return err
	}
	selects := []string{"?::bigint", "?::text", "kind", "source_id", "key_id", "provider", "model", "upstream_model", "endpoint", "stream", "outcome"}
	for _, name := range strings.Split(rollupSums, ",") {
		selects = append(selects, "sum("+name+")")
	}
	for _, name := range []string{"first_bins", "dispatch_bins", "created_bins", "text_bins"} {
		selects = append(selects, mergeHistogramSQL(name))
	}
	selects = append(selects, "max(last_ms)")
	for _, name := range []string{"first", "dispatch", "created", "text"} {
		value, clock := "last_"+name, "last_"+name+"_ms"
		selects = append(selects, "(max(ARRAY["+clock+"::DOUBLE PRECISION,"+value+"]) FILTER(WHERE "+value+" IS NOT NULL AND "+clock+" IS NOT NULL))[2]")
	}
	for _, name := range []string{"first", "dispatch", "created", "text"} {
		selects = append(selects, "max(last_"+name+"_ms)")
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO rollups("+rollupColumns()+") SELECT "+strings.Join(selects, ",")+" FROM rollups WHERE level='minute' AND period_ms>=? AND period_ms<? GROUP BY kind,source_id,key_id,provider,model,upstream_model,endpoint,stream,outcome", start, level, start, end)
	return err
}

func rollupColumns() string {
	return rollupDimensions + "," + rollupSums + ",first_bins,dispatch_bins,created_bins,text_bins,last_ms,last_first,last_dispatch,last_created,last_text,last_first_ms,last_dispatch_ms,last_created_ms,last_text_ms"
}

// Only the newly inserted, deduplicated facts contribute to these deltas.
// No day/history scan is performed for an arriving batch.
func factRollupSelect(source string) string {
	dims := "period_ms,level,kind,coalesce(source_id,'') AS source_id,coalesce(key_id,'') AS key_id,coalesce(provider,'') AS provider,coalesce(model,'') AS model,coalesce(upstream_model,'') AS upstream_model,coalesce(endpoint,'') AS endpoint,coalesce(stream,false) AS stream,coalesce(outcome,'') AS outcome"
	return `SELECT ` + dims + `,count(*),sum(coalesce(input_tokens,0)),sum(coalesce(output_tokens,0)),sum(coalesce(cache_read_tokens,0)),sum(coalesce(cache_write_tokens,0)),sum(coalesce(cache_write_1h_tokens,0)),count(input_tokens),count(cache_read_tokens),sum(coalesce(actual_cost_usd,0)),count(actual_cost_usd),count(first_output_ms),count(dispatch_ms),sum(coalesce(first_output_ms,0)),sum(coalesce(dispatch_ms,0)),count(response_created_ms),sum(coalesce(response_created_ms,0)),count(first_text_ms),sum(coalesce(first_text_ms,0)),` +
		histogramSQL("first_output_ms") + `,` + histogramSQL("dispatch_ms") + `,` + histogramSQL("response_created_ms") + `,` + histogramSQL("first_text_ms") + `,
		max(at_ms),(max(ARRAY[at_ms::DOUBLE PRECISION,first_output_ms]) FILTER(WHERE first_output_ms IS NOT NULL AND at_ms IS NOT NULL))[2],(max(ARRAY[at_ms::DOUBLE PRECISION,dispatch_ms]) FILTER(WHERE dispatch_ms IS NOT NULL AND at_ms IS NOT NULL))[2],(max(ARRAY[at_ms::DOUBLE PRECISION,response_created_ms]) FILTER(WHERE response_created_ms IS NOT NULL AND at_ms IS NOT NULL))[2],(max(ARRAY[at_ms::DOUBLE PRECISION,first_text_ms]) FILTER(WHERE first_text_ms IS NOT NULL AND at_ms IS NOT NULL))[2],max(at_ms) FILTER(WHERE first_output_ms IS NOT NULL),max(at_ms) FILTER(WHERE dispatch_ms IS NOT NULL),max(at_ms) FILTER(WHERE response_created_ms IS NOT NULL),max(at_ms) FILTER(WHERE first_text_ms IS NOT NULL) FROM ` + source + ` WHERE kind NOT IN ('trace','billing') GROUP BY ` + rollupDimensions
}

func rollupMerge() string {
	updates := []string{}
	for _, name := range strings.Split(rollupSums, ",") {
		updates = append(updates, name+"=rollups."+name+"+excluded."+name)
	}
	for _, name := range []string{"first_bins", "dispatch_bins", "created_bins", "text_bins"} {
		bins := make([]string, len(histogramBounds))
		for i := range bins {
			bins[i] = fmt.Sprintf("rollups.%s[%d]+excluded.%s[%d]", name, i+1, name, i+1)
		}
		updates = append(updates, name+"=ARRAY["+strings.Join(bins, ",")+"]")
	}
	updates = append(updates, "last_ms=greatest(rollups.last_ms,excluded.last_ms)")
	for _, name := range []string{"first", "dispatch", "created", "text"} {
		value := "last_" + name
		clock := value + "_ms"
		updates = append(updates, value+"=CASE WHEN (excluded."+clock+">coalesce(rollups."+clock+",0) OR (excluded."+clock+"=rollups."+clock+" AND excluded."+value+">=rollups."+value+")) THEN excluded."+value+" ELSE rollups."+value+" END", clock+"=greatest(rollups."+clock+",excluded."+clock+")")
	}
	return " ON CONFLICT(" + rollupDimensions + ") DO UPDATE SET " + strings.Join(updates, ",")
}

func ensureRollupPartition(ctx context.Context, tx *analyticsTx, at int64) error {
	start := at / 3600000 * 3600000
	name := fmt.Sprintf("rollups_%d", start)
	var exists bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM rollup_partitions WHERE start_ms=?)", start).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	if err := createRollupHour(ctx, tx, start); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO rollup_partitions(start_ms,end_ms,name) VALUES(?,?,?)", start, start+3600000, name)
	return err
}

func createRollupHour(ctx context.Context, tx *analyticsTx, start int64) error {
	name := fmt.Sprintf("rollups_%d", start)
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("CREATE TABLE %s PARTITION OF rollups FOR VALUES FROM (%d) TO (%d) PARTITION BY LIST(level)", name, start, start+3600000)); err != nil {
		return err
	}
	for _, level := range []string{"minute", "hour", "day"} {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("CREATE TABLE %s_%s PARTITION OF %s FOR VALUES IN ('%s')", name, level, name, level)); err != nil {
			return err
		}
	}
	return nil
}
