package main

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCapabilitySummaryPreservesTypedResultsAndErrors(t *testing.T) {
	s := managementBindingTestService(t)
	cases := []string{
		`null`, `{}`, `{"attempts":null,"models":[null,{"model":"m","result":null}]}`,
		`{"models":[{"model":"m","state":"done","result":{"status":"success","attempts":[null,{"text":"discarded","request_ids":["r",null],"http_status":200,"usage":{"status":"matched","input_price":0}}]}}]}`,
		`{"attempts":[{"http_status":"bad"}]}`, `{"attempts":[{"text":42}]}`, `{"attempts":[{"request_ids":[42]}]}`,
		`{"attempts":"bad"}`, `{"models":[1]}`, `{"models":[{"result":42}]}`, `{"checked_at":1.5}`,
		`{"STATUS":"legacy","Attempts":[{"Text":"legacy"}]}`, `{"status":"success","future":42}`,
		`{"attempts":[{"usage":{"input_price":1e1000}}]}`, `{"checked_at":9223372036854775807}`,
	}
	capability := subCapabilityResult{Status: "success", Model: "m", Message: strings.Repeat("消息🌈", 100), Attempts: []subProbe{{ID: "private", Text: strings.Repeat("reply", 10000), StartedAt: math.MaxInt64, Usage: &subUsage{ActualCost: usageFloat(0), InputTokens: usageInt(0)}}}, Models: []subToolUseModel{{Model: "m", State: "done", Result: &subCapabilityResult{Status: "success", Attempts: []subProbe{{Text: "nested reply"}}}}}}
	cases = append(cases, mustJSON(capability))
	for _, raw := range cases {
		var gotJSON []byte
		if err := s.control.db.QueryRowContext(context.Background(), `SELECT console_sub_capability_summary_v1($1::jsonb)`, raw).Scan(&gotJSON); err != nil {
			t.Fatal(err)
		}
		var want, got *subCapabilityResult
		wantErr := json.Unmarshal([]byte(raw), &want)
		gotErr := json.Unmarshal(gotJSON, &got)
		if (wantErr == nil) != (gotErr == nil) {
			t.Fatalf("changed decoder error: %s old=%v new=%v", raw, wantErr, gotErr)
		}
		if wantErr != nil {
			continue
		}
		if !reflect.DeepEqual(compactSummary(want), compactSummary(got)) {
			t.Fatalf("summary changed %s", raw)
		}
	}
}

func TestCapabilitySummaryProductionCardinality(t *testing.T) {
	if os.Getenv("RUN_MANAGEMENT_SIGNALS_SCALE") != "1" {
		t.Skip("opt-in synthetic scale")
	}
	s := managementBindingTestService(t)
	db := s.control.db
	if _, err := db.Exec(`CREATE TABLE capability_fixture(id INTEGER PRIMARY KEY,result JSONB);INSERT INTO capability_fixture SELECT g,jsonb_build_object('status','success','model','m','checked_at',42,'attempts',jsonb_build_array(jsonb_build_object('text',repeat('receipt detail ',1200),'status','success','usage',jsonb_build_object('input_price',0)))) FROM generate_series(1,749) g`); err != nil {
		t.Fatal(err)
	}
	var baseline []any
	for _, projected := range []bool{false, true, false, true} {
		column := "result"
		if projected {
			column = "console_sub_capability_summary_v1(result)"
		}
		start := time.Now()
		rows, err := db.Query(`SELECT ` + column + ` FROM capability_fixture ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		bytes := 0
		values := []any{}
		for rows.Next() {
			var raw []byte
			var result *subCapabilityResult
			if err := rows.Scan(&raw); err != nil {
				t.Fatal(err)
			}
			bytes += len(raw)
			if err := json.Unmarshal(raw, &result); err != nil {
				t.Fatal(err)
			}
			values = append(values, compactSummary(result))
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		if baseline == nil {
			baseline = values
		} else if !reflect.DeepEqual(values, baseline) {
			t.Fatal("scale values changed")
		}
		t.Logf("projected=%t rows=%d bytes=%d elapsed_ms=%d", projected, len(values), bytes, time.Since(start).Milliseconds())
	}
}

func TestCapabilitySummaryManagementAndDetailRemainEquivalent(t *testing.T) {
	s := managementHydrationFixture(t)
	db, ctx := s.control.db, context.Background()
	value := subCapabilityResult{Status: "success", Model: "m", CheckedAt: 42, Attempts: []subProbe{{ID: "evidence", Text: strings.Repeat("keep in detail", 1000)}}}
	if _, err := db.Exec(`UPDATE console_sub_targets SET tool_use=$1,compaction=$1 WHERE group_id=1`, mustJSON(value)); err != nil {
		t.Fatal(err)
	}
	read := func(c context.Context, compact bool) []subAccount {
		t.Helper()
		accounts, err := readSubAccountHeaders(c, db, "owner", "")
		if err != nil {
			t.Fatal(err)
		}
		if err = s.readSubAccountDetailsWithModelsFrom(c, db, "owner", "", 0, "", true, true, accounts, nil, sharedQuality{}, nil, nil, compact); err != nil {
			t.Fatal(err)
		}
		return accounts
	}
	full, summary := read(ctx, false), read(ctx, true)
	if len(full[0].Targets[0].ToolUse.Attempts) == 0 || len(summary[0].Targets[0].ToolUse.Attempts) != 0 {
		t.Fatal("detail evidence lost or summary not compacted")
	}
	if err := normalizeManagementAccounts(full); err != nil {
		t.Fatal(err)
	}
	if err := normalizeManagementAccounts(summary); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(full, summary) {
		t.Fatal("management output changed")
	}
}
