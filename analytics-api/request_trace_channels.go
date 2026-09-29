package main

import (
	"context"
	"encoding/json"
)

// Current, owner-scoped channel labels are presentation metadata, not claims
// about the rate or configuration at the historical request's timestamp.
type TraceChannel struct {
	Name         string   `json:"name"`
	SiteName     string   `json:"site_name,omitempty"`
	GroupName    string   `json:"group_name,omitempty"`
	GroupID      int64    `json:"group_id,omitempty"`
	Rate         *float64 `json:"rate"`
	DashboardURL string   `json:"dashboard_url,omitempty"`
}

func (s *Service) enrichTraceChannels(ctx context.Context, owner string, runs []RequestTrace) error {
	if len(runs) == 0 {
		return nil
	}
	rows, err := s.control.db.QueryContext(ctx, `SELECT a.id,a.name,a.base,t.group_id,t.name,t.billing FROM console_sub_accounts a JOIN console_sub_targets t ON t.account_id=a.id WHERE a.owner=$1`, owner)
	if err != nil {
		return err
	}
	type group struct {
		account string
		channel TraceChannel
	}
	groups := []group{}
	byGroup := map[string]TraceChannel{}
	for rows.Next() {
		var g group
		var base string
		var billing []byte
		if err = rows.Scan(&g.account, &g.channel.SiteName, &base, &g.channel.GroupID, &g.channel.GroupName, &billing); err != nil {
			break
		}
		g.channel.Name = subChannelDisplayName(g.channel.SiteName, billing)
		g.channel.DashboardURL = dashboardURL(base)
		var b subBilling
		if json.Unmarshal(billing, &b) == nil {
			g.channel.Rate = b.Rate
		}
		groups = append(groups, g)
		byGroup[qualityGroupID(g.account, g.channel.GroupID)] = g.channel
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	bindings, err := s.configuredBindings(ctx, owner)
	if err != nil {
		return err
	}
	bySource := map[string][]subInstalledChannel{}
	for _, b := range bindings {
		bySource[b.SourceID] = append(bySource[b.SourceID], b)
	}
	for i := range runs {
		run := &runs[i]
		run.Channels = map[string]TraceChannel{}
		wanted, keys := map[string]bool{}, map[string]bool{}
		for _, e := range run.Events {
			if e.Provider != "" {
				wanted[e.Provider] = true
			}
			if e.KeyID != "" {
				keys[e.KeyID] = true
			}
		}
		put := func(provider string, c TraceChannel) {
			if wanted[provider] {
				run.Channels[provider] = c
			}
		}
		// The exact request key and stable account/group hash also identify
		// temporary channels that have since been removed. No gateway API calls.
		for key := range keys {
			for _, g := range groups {
				put(subProviderName(g.account, g.channel.GroupID, key), g.channel)
			}
		}
		for _, b := range bySource[run.SourceID] {
			c, ok := byGroup[qualityGroupID(b.AccountID, b.GroupID)]
			if !ok {
				c = TraceChannel{Name: b.Name, DashboardURL: dashboardURL(b.Base)}
			}
			// Never replace an exact owned group match with an unbound label.
			if _, exists := run.Channels[b.Provider]; !exists {
				put(b.Provider, c)
			}
			for key := range keys {
				put(configuredImportName(b.Provider, key), c)
			}
		}
	}
	return nil
}
