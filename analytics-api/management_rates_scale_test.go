package main

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestManagementPreparedRatesScale(t *testing.T) {
	if os.Getenv("RUN_OBSERVED_RATE_SCALE") != "1" {
		t.Skip("opt-in isolated scale comparison")
	}
	s := managementBindingTestService(t)
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) SELECT 'rates-'||a,'owner','fixture','https://fixture.test',a::text,'' FROM generate_series(0,47) a`,
		`INSERT INTO console_sub_targets(account_id,group_id,name,platform,remote_key_id) SELECT 'rates-'||(g%48),g,'group','openai',42 FROM generate_series(1,749) g`,
		`INSERT INTO console_sub_usage(id,account_id,group_id,key_id,model,started_at,request_ids,status,result) SELECT 'rates-'||g||'-'||m,'rates-'||(g%48),g,42,'m'||lpad(m::text,3,'0'),m,'[]','matched',jsonb_build_object('status','matched','input_price',2,'total_cost',1,'actual_cost',m*0.01,'message',repeat('wide diagnostic ',200)) FROM generate_series(1,749) g CROSS JOIN generate_series(1,80) m`,
		`ANALYZE console_sub_usage`, `ANALYZE console_sub_observed_rates`, `ANALYZE console_sub_targets`, `ANALYZE console_sub_accounts`,
	} {
		if _, err := s.control.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.control.db.Exec(`UPDATE console_management_projection_state SET phase='complete',complete=true WHERE singleton`); err != nil {
		t.Fatal(err)
	}
	read := func(mode string) (map[string]map[string]subObservedRate, time.Duration) {
		t.Helper()
		start := time.Now()
		var got map[string]map[string]subObservedRate
		var err error
		switch mode {
		case "legacy":
			got, err = s.control.subObservedRatesForAccountFrom(ctx, s.control.db, "owner", "")
		case "previous":
			got, err = s.control.subObservedRatesPreparedFrom(ctx, previousRateQueryDB{s.control.db}, "owner", "")
		case "merged":
			got, err = s.control.subObservedRatesPreparedFrom(ctx, s.control.db, "owner", "")
		}
		if err != nil {
			t.Fatal(err)
		}
		return got, time.Since(start)
	}
	for round, order := range [][]string{{"legacy", "previous", "merged"}, {"merged", "previous", "legacy"}, {"previous", "merged", "legacy"}, {"merged", "legacy", "previous"}} {
		values := map[string]map[string]map[string]subObservedRate{}
		durations := map[string]int64{}
		for _, mode := range order {
			value, elapsed := read(mode)
			values[mode] = value
			durations[mode] = elapsed.Milliseconds()
		}
		if len(values["legacy"]) != 749 || !reflect.DeepEqual(values["legacy"], values["previous"]) || !reflect.DeepEqual(values["legacy"], values["merged"]) {
			t.Fatal("latest rates changed")
		}
		t.Logf("round=%d order=%v groups=749 legacy_ms=%d previous_projected_ms=%d merged_ms=%d", round, order, durations["legacy"], durations["previous"], durations["merged"])
	}
	if os.Getenv("EXPLAIN_OBSERVED_RATE_MERGE") == "1" {
		for _, variant := range []struct{ name, query string }{{"previous", previousObservedRatesReadSQL(true)}, {"merged", subObservedRatesReadSQL(true)}} {
			var raw []byte
			if err := s.control.db.QueryRowContext(ctx, `EXPLAIN (ANALYZE,BUFFERS,TIMING FALSE,FORMAT JSON) `+variant.query, "owner", "").Scan(&raw); err != nil {
				t.Fatal(err)
			}
			t.Logf("rate_merge_plan variant=%s plan=%s", variant.name, raw)
		}
	}
}
