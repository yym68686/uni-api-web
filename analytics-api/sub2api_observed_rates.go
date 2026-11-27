package main

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type subObservedRate struct {
	Rate       float64 `json:"rate"`
	ObservedAt int64   `json:"observed_at"`
}

// Read current receipt-derived metadata independently from model payloads.
// Historical request FIFO must not erase the last verified model multiplier.
func (s *Service) subAccountRatesSummary(w http.ResponseWriter, r *http.Request, owner string) {
	observed, err := s.control.subObservedRates(r.Context(), owner)
	if err != nil {
		http.Error(w, "账单倍率读取失败", http.StatusServiceUnavailable)
		return
	}
	rows := make([]summaryRow, 0, len(observed))
	for group, rates := range observed {
		rows = append(rows, summaryRow{ID: group, Value: rates})
	}
	s.writeSummary(w, r, rows)
}

func subReceiptRate(usage subUsage, startedAt int64) *subObservedRate {
	if usage.Status != "matched" || subValidCost(usage.ActualCost) == nil || subValidCost(usage.TotalCost) == nil || *usage.TotalCost == 0 {
		return nil
	}
	if subValidCost(usage.InputPrice) == nil && subValidCost(usage.OutputPrice) == nil {
		return nil
	}
	rate := *usage.ActualCost / *usage.TotalCost
	if subValidCost(usage.Rate) != nil && math.Abs(*usage.ActualCost-*usage.TotalCost**usage.Rate) <= 1e-10+math.Abs(*usage.ActualCost)*1e-9 {
		rate = *usage.Rate
	}
	if subValidCost(&rate) == nil {
		return nil
	}
	if at, err := time.Parse(time.RFC3339Nano, usage.CreatedAt); err == nil {
		startedAt = at.Unix()
	}
	return &subObservedRate{Rate: rate, ObservedAt: startedAt}
}

func (s *controlStore) subObservedRates(ctx context.Context, owner string) (map[string]map[string]subObservedRate, error) {
	return s.subObservedRatesForAccount(ctx, owner, "")
}

func (s *controlStore) subObservedRatesForAccount(ctx context.Context, owner, accountID string) (map[string]map[string]subObservedRate, error) {
	return s.subObservedRatesForAccountFrom(ctx, s.db, owner, accountID)
}

// Payload selection never changes current-key or latest-receipt membership.
// The prepared variant is used only inside the management snapshot after its
// bounded projection verification. Any unsupported candidate falls back before
// publishing, preserving the original decoder (including legacy field casing).
func subObservedRatesReadSQL(prepared bool) string {
	if prepared {
		return managementPreparedRateSQL
	}
	return `WITH owned_targets AS MATERIALIZED (
 SELECT t.account_id,t.group_id,t.remote_key_id FROM console_sub_targets t JOIN console_sub_accounts a ON a.id=t.account_id WHERE a.owner=$1 AND ($2='' OR a.id=$2)),
 raw_latest AS (
 SELECT DISTINCT ON (u.account_id,u.group_id,u.model) u.id,u.account_id,u.group_id,u.model,u.started_at
 FROM console_sub_usage u JOIN owned_targets t ON t.account_id=u.account_id AND t.group_id=u.group_id AND t.remote_key_id=u.key_id
 WHERE u.status='matched' ORDER BY u.account_id,u.group_id,u.model,u.started_at DESC,u.id DESC),
 candidates AS (
 SELECT l.id,l.account_id,l.group_id,l.model,l.started_at,u.result AS result
 FROM raw_latest l JOIN console_sub_usage u ON u.id=l.id
 UNION ALL
 SELECT r.receipt_id,r.account_id,r.group_id,r.model,r.started_at,r.result
 FROM console_sub_observed_rates r JOIN owned_targets t ON t.account_id=r.account_id AND t.group_id=r.group_id AND t.remote_key_id=r.key_id)
 SELECT DISTINCT ON (account_id,group_id,model) account_id,group_id,model,started_at,result
 FROM candidates ORDER BY account_id,group_id,model,started_at DESC,id DESC`
}

func (s *controlStore) subObservedRatesPreparedFrom(ctx context.Context, db controlQueryDB, owner, accountID string) (map[string]map[string]subObservedRate, error) {
	var complete bool
	if err := db.QueryRowContext(ctx, `SELECT complete FROM console_management_projection_state WHERE singleton`).Scan(&complete); err != nil {
		return nil, err
	}
	if !complete {
		return s.subObservedRatesForAccountFrom(ctx, db, owner, accountID)
	}
	result, err := readManagementRates(ctx, db, s.db, owner, accountID, func() (map[string]map[string]subObservedRate, error) {
		return readSubObservedRatesQuery(ctx, db, owner, accountID, true)
	})
	if err == errManagementPredicateFallback {
		return s.subObservedRatesForAccountFrom(ctx, db, owner, accountID)
	}
	return result, err
}

func (s *controlStore) subObservedRatesForAccountFrom(ctx context.Context, db controlQueryDB, owner, accountID string) (map[string]map[string]subObservedRate, error) {
	return readSubObservedRatesQuery(ctx, db, owner, accountID, false)
}

func readSubObservedRatesQuery(ctx context.Context, db controlQueryDB, owner, accountID string, prepared bool) (map[string]map[string]subObservedRate, error) {
	stage := "rate_raw_rows"
	if prepared {
		stage = "rate_rows"
	}
	trace := newManagementRowsTrace(ctx, stage)
	defer trace.finish()
	rows, err := db.QueryContext(ctx, subObservedRatesReadSQL(prepared), owner, accountID)
	if err != nil {
		return nil, err
	}
	trace.queryReady()
	defer rows.Close()
	out := map[string]map[string]subObservedRate{}
	for trace.next(rows) {
		var account, model string
		var group, startedAt int64
		var raw []byte
		var requiresRaw bool
		fields := []any{&account, &group, &model, &startedAt, &raw}
		if prepared {
			fields = append(fields, &requiresRaw)
		}
		if err := rows.Scan(fields...); err != nil {
			return nil, err
		}
		if requiresRaw {
			return nil, errManagementPredicateFallback
		}
		var usage subUsage
		start := trace.decodeStart()
		decodeErr := json.Unmarshal(raw, &usage)
		trace.decoded(start, len(raw))
		if decodeErr != nil {
			continue
		}
		rate := subReceiptRate(usage, startedAt)
		if rate == nil {
			continue
		}
		key := qualityGroupID(account, group)
		if out[key] == nil {
			out[key] = map[string]subObservedRate{}
		}
		out[key][model] = *rate
	}
	return out, rows.Err()
}

type subChannelModelRate struct {
	Name       string   `json:"name"`
	Rate       *float64 `json:"rate"`
	RateSource string   `json:"rate_source,omitempty"`
	ObservedAt int64    `json:"observed_at,omitempty"`
}

func subChannelModelRates(channels []subInstalledChannel, refs []subChannelRef, business map[subBusinessRateKey]map[string]subObservedRate, now time.Time) map[string]map[string]map[string]subChannelModelRate {
	byGroup := map[string]subChannelRef{}
	for _, ref := range refs {
		byGroup[qualityGroupID(ref.Account, ref.Group)] = ref
	}
	// A provider may occur on more than one caller key. Its display metadata is
	// provider-scoped, so a fallback must be unambiguous across all those rows.
	providerGroups := map[string]map[string]bool{}
	for _, channel := range channels {
		key := channel.SourceID + "\n" + channel.Provider
		if providerGroups[key] == nil {
			providerGroups[key] = map[string]bool{}
		}
		if channel.Kind != "configured" {
			providerGroups[key][qualityGroupID(channel.AccountID, channel.GroupID)] = true
		} else if channel.BindingStatus == "matched" {
			for _, bound := range channel.BoundKeys {
				providerGroups[key][qualityGroupID(bound.AccountID, bound.GroupID)] = true
			}
		}
	}
	out := map[string]map[string]map[string]subChannelModelRate{}
	for _, channel := range channels {
		type member struct {
			subChannelRef
			key int64
		}
		groups := []member{}
		if channel.Kind != "configured" {
			if ref, ok := byGroup[qualityGroupID(channel.AccountID, channel.GroupID)]; ok {
				groups = append(groups, member{ref, ref.RoutingKey})
			}
		} else if channel.BindingStatus == "matched" {
			for _, key := range channel.BoundKeys {
				ref, ok := byGroup[qualityGroupID(key.AccountID, key.GroupID)]
				if !ok {
					groups = nil
					break
				}
				groups = append(groups, member{ref, key.RemoteKeyID})
			}
		}
		if len(groups) == 0 {
			continue
		}
		names := map[string]bool{}
		for _, group := range groups {
			names[group.AccountName] = true
		}
		accountNames := make([]string, 0, len(names))
		for name := range names {
			accountNames = append(accountNames, name)
		}
		sort.Strings(accountNames)
		prefix := strings.Join(accountNames, " / ")
		if out[channel.SourceID] == nil {
			out[channel.SourceID] = map[string]map[string]subChannelModelRate{}
		}
		if out[channel.SourceID][channel.Provider] == nil {
			out[channel.SourceID][channel.Provider] = map[string]subChannelModelRate{}
		}
		models := out[channel.SourceID][channel.Provider]
		for _, model := range channel.Models {
			if upstream := channel.ModelMappings[model]; upstream != "" {
				model = upstream
			}
			value := subChannelModelRate{}
			for index, group := range groups {
				receipt, found := business[subBusinessRateKey{group.Account, group.Group, group.key}][model]
				detection, detected := group.ObservedRates[model]
				candidate := subChannelModelRate{}
				switch {
				case group.key > 0 && found:
					rate := adjustedSiteAmount(receipt.Rate, group.RateCoefficient)
					candidate = subChannelModelRate{Rate: subValidCost(&rate), RateSource: "business", ObservedAt: receipt.ObservedAt}
				case detected && detection.ObservedAt >= businessRateCutoff(now)/1000 && detection.ObservedAt <= now.Unix():
					candidate = subChannelModelRate{Rate: &detection.Rate, RateSource: "observed", ObservedAt: detection.ObservedAt}
				case len(providerGroups[channel.SourceID+"\n"+channel.Provider]) == 1 && subValidCost(group.Rate) != nil:
					candidate = subChannelModelRate{Rate: group.Rate, RateSource: "advertised"}
				case detected && detection.ObservedAt <= now.Unix():
					candidate = subChannelModelRate{Rate: &detection.Rate, RateSource: "historical", ObservedAt: detection.ObservedAt}
				}
				if index == 0 {
					value = candidate
				} else {
					value = mergeSubChannelRates(value, candidate)
				}
				if value.Rate == nil {
					value = subChannelModelRate{}
					break
				}
			}
			if previous, exists := models[model]; exists {
				value = mergeSubChannelRates(previous, value)
			}
			value.Name = prefix + "-未知倍率"
			if value.Rate != nil {
				value.Name = prefix + "-" + strconv.FormatFloat(*value.Rate, 'f', -1, 64)
			}
			models[model] = value
		}
	}
	return out
}

func mergeSubChannelRates(a, b subChannelModelRate) subChannelModelRate {
	if a.Rate == nil || b.Rate == nil || math.Abs(*a.Rate-*b.Rate) > 1e-9 {
		return subChannelModelRate{}
	}
	if a.RateSource != b.RateSource {
		a.RateSource = "mixed"
	}
	a.ObservedAt = min(a.ObservedAt, b.ObservedAt)
	return a
}
