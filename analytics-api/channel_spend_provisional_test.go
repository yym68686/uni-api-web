package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestHistoryLimitedSubtotalProgressesWithoutInventingABound(t *testing.T) {
	s, account, owner := attributionFixture(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	from, to := now.Add(-time.Hour).Unix(), now.Unix()
	paid := attributedFact("paid", "caller", now.Add(-time.Minute))
	delayed := attributedFact("delayed", "caller", now.Add(-time.Minute))
	conflict := attributedFact("conflict", "caller", now.Add(-time.Minute))
	hidden := conflict
	hidden.EventID, hidden.SourceID, hidden.KeyID, hidden.Provider = "hidden", "hidden-source", "other-caller", "hidden-provider"
	if err := s.engine.Import(ctx, "provisional", "one", []Fact{paid, delayed, conflict, hidden}); err != nil {
		t.Fatal(err)
	}
	storeAttributedLog(t, s, account, "paid", "0.25", 1)
	storeAttributedLog(t, s, account, "conflict", "99", 2)
	if _, err := s.engine.DB.Exec("UPDATE storage_state SET claims_history_complete=false WHERE singleton"); err != nil {
		t.Fatal(err)
	}
	filter := QueryFilter{SourceID: "source", KeyID: "caller", Provider: "shared"}
	check := func(user, reason string, want *float64, matched, pending int) {
		t.Helper()
		rows, err := s.attributedChannelSpend(ctx, user, filter, from, to)
		if err != nil || len(rows) != 1 {
			t.Fatal(rows, err)
		}
		row := rows[0]
		if row.Status != "history_limited" || row.HistoryReason != reason || row.Amount != nil || row.Matched != matched || row.Pending != pending || (user == owner && row.Ambiguous != 1) {
			t.Fatal("history or conflict evidence changed", row)
		}
		body, err := json.Marshal(map[string]any{"channel_spend": rows})
		if err != nil {
			t.Fatal(err)
		}
		var wire struct {
			Rows []map[string]any `json:"channel_spend"`
		}
		if err = json.Unmarshal(body, &wire); err != nil {
			t.Fatal(err)
		}
		got := wire.Rows[0]
		if _, present := got["matched_cost_usd"]; present || got["actual_cost_usd"] != nil {
			t.Fatal("unproven total or lower bound exposed", got)
		}
		value, present := got["observed_cost_usd"]
		if (want != nil) != present || (want != nil && value != *want) {
			t.Fatal("provisional subtotal changed or leaked", got)
		}
	}
	first, final := .25, 1.0
	check(owner, "ownership_history_incomplete", &first, 1, 1)
	storeAttributedLog(t, s, account, "delayed", "0.75", 3)
	check(owner, "ownership_history_incomplete", &final, 2, 0)
	check("foreign-owner", "ownership_history_incomplete", nil, 0, 0)
	// A complete ownership index does not restore requests outside FIFO. Keep
	// this distinct from the permanently incomplete historical ownership case.
	if _, err := s.engine.DB.Exec("UPDATE storage_state SET claims_history_complete=true,evicted_before_ms=? WHERE singleton", from*1000+1); err != nil {
		t.Fatal(err)
	}
	check(owner, "requested_range_evicted", &final, 2, 0)
}

func TestProvisionalWireAmountRequiresCurrentMatchedEvidence(t *testing.T) {
	for _, tc := range []struct {
		status            string
		matched, unbilled int
		cost              float64
		present           bool
	}{
		{"history_limited", 1, 0, 5, true},
		{"history_limited", 1, 0, 0, true},
		{"history_limited", 0, 1, 0, true},
		{"history_limited", 0, 0, 5, false},
		{"complete", 1, 0, 5, false},
		{"pending", 1, 0, 5, false},
	} {
		body, err := json.Marshal(attributedSpend{Status: tc.status, Matched: tc.matched, ConfirmedUnbilled: tc.unbilled, MatchedAmount: tc.cost})
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err = json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		value, present := got["observed_cost_usd"]
		if present != tc.present || (present && value != tc.cost) {
			t.Fatal(tc, got)
		}
	}
}
