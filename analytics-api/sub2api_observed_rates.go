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

func (s *controlStore) subObservedRatesForAccountFrom(ctx context.Context, db controlQueryDB, owner, accountID string) (map[string]map[string]subObservedRate, error) {
	rows, err := db.QueryContext(ctx, `WITH owned_targets AS MATERIALIZED (
 SELECT t.account_id,t.group_id,t.remote_key_id FROM console_sub_targets t JOIN console_sub_accounts a ON a.id=t.account_id WHERE a.owner=$1 AND ($2='' OR a.id=$2)),
 raw_latest AS (
 SELECT DISTINCT ON (u.account_id,u.group_id,u.model) u.id,u.account_id,u.group_id,u.model,u.started_at
 FROM console_sub_usage u JOIN owned_targets t ON t.account_id=u.account_id AND t.group_id=u.group_id AND t.remote_key_id=u.key_id
 WHERE u.status='matched' ORDER BY u.account_id,u.group_id,u.model,u.started_at DESC,u.id DESC),
 candidates AS (
 SELECT l.id,l.account_id,l.group_id,l.model,l.started_at,u.result
 FROM raw_latest l JOIN console_sub_usage u ON u.id=l.id
 UNION ALL
 SELECT r.receipt_id,r.account_id,r.group_id,r.model,r.started_at,r.result
 FROM console_sub_observed_rates r JOIN owned_targets t ON t.account_id=r.account_id AND t.group_id=r.group_id AND t.remote_key_id=r.key_id)
 SELECT DISTINCT ON (account_id,group_id,model) account_id,group_id,model,started_at,result
 FROM candidates ORDER BY account_id,group_id,model,started_at DESC,id DESC`, owner, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]subObservedRate{}
	for rows.Next() {
		var account, model string
		var group, startedAt int64
		var raw []byte
		if err := rows.Scan(&account, &group, &model, &startedAt, &raw); err != nil {
			return nil, err
		}
		var usage subUsage
		if json.Unmarshal(raw, &usage) != nil {
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
	ObservedAt int64    `json:"observed_at,omitempty"`
}

func subChannelModelRates(channels []subInstalledChannel, refs []subChannelRef) map[string]map[string]map[string]subChannelModelRate {
	byGroup := map[string]subChannelRef{}
	for _, ref := range refs {
		byGroup[qualityGroupID(ref.Account, ref.Group)] = ref
	}
	out := map[string]map[string]map[string]subChannelModelRate{}
	for _, channel := range channels {
		groups := []subChannelRef{}
		if channel.Kind != "configured" {
			if ref, ok := byGroup[qualityGroupID(channel.AccountID, channel.GroupID)]; ok {
				groups = append(groups, ref)
			}
		} else if channel.BindingStatus == "matched" {
			for _, key := range channel.BoundKeys {
				ref, ok := byGroup[qualityGroupID(key.AccountID, key.GroupID)]
				if !ok {
					groups = nil
					break
				}
				groups = append(groups, ref)
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
			value := subChannelModelRate{Name: prefix + "-未知倍率"}
			for index, group := range groups {
				rate, ok := group.ObservedRates[model]
				if !ok || (index > 0 && value.Rate != nil && math.Abs(*value.Rate-rate.Rate) > 1e-9) {
					value.Rate = nil
					value.ObservedAt = 0
					break
				}
				value.Rate = &rate.Rate
				if index == 0 || rate.ObservedAt < value.ObservedAt {
					value.ObservedAt = rate.ObservedAt
				}
			}
			if previous, exists := models[model]; exists && (previous.Rate == nil || value.Rate == nil || math.Abs(*previous.Rate-*value.Rate) > 1e-9) {
				value.Rate = nil
				value.ObservedAt = 0
			}
			if value.Rate != nil {
				value.Name = prefix + "-" + strconv.FormatFloat(*value.Rate, 'f', -1, 64)
			}
			models[model] = value
		}
	}
	return out
}
