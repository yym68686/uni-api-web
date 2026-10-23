package main

type managementPageContext struct {
	Prices            []Price                                 `json:"prices"`
	Imports           []subInstalledChannel                   `json:"imports"`
	Routes            map[string]managementQueryRoutes        `json:"routes"`
	Fingerprints      map[string][]managementFingerprintEntry `json:"fingerprints"`
	FingerprintModels []string                                `json:"fingerprint_models"`
}

// Display metadata comes from the same read as the page. Limit route/import
// records to its visible groups/providers instead of starting independent UI
// reads that can observe another configuration generation.
func managementContextForPage(view managementDatabaseView, page managementQueryPage, model string) managementPageContext {
	out := managementPageContext{Prices: view.Prices, Imports: []subInstalledChannel{}, Routes: map[string]managementQueryRoutes{}, Fingerprints: map[string][]managementFingerprintEntry{}, FingerprintModels: managementFingerprintModels}
	providers, groups := map[string]bool{}, map[string]bool{}
	index := managementSharedFingerprints(view.Input)
	for _, row := range page.Rows {
		if row.Account.ID != "" {
			groups[qualityGroupID(row.Account.ID, row.Target.GroupID)] = true
		}
		if row.Configured != nil {
			for _, member := range row.Configured.Members {
				providers[qualityChannelID(member.SourceID, member.Provider)] = true
			}
		}
		out.Fingerprints[row.ID] = managementRowFingerprintEntries(row, index, model)
	}
	for _, channel := range view.Input.Imports {
		include := providers[qualityChannelID(channel.SourceID, channel.Provider)]
		for _, binding := range managementBoundGroups(managementChannel{subInstalledChannel: channel}) {
			include = include || groups[qualityGroupID(binding.AccountID, binding.GroupID)]
		}
		if include {
			out.Imports = append(out.Imports, channel)
			providers[qualityChannelID(channel.SourceID, channel.Provider)] = true
		}
	}
	for source, routes := range view.Input.Routes {
		filtered := routes
		filtered.Data = []channelRoute{}
		for _, route := range routes.Data {
			if providers[qualityChannelID(source, route.Provider)] || providers[qualityChannelID(source, route.OriginProvider)] {
				filtered.Data = append(filtered.Data, route)
			}
		}
		out.Routes[source] = filtered
	}
	return out
}

func managementAccountHeaders(accounts []subAccount) []subAccount {
	out := make([]subAccount, len(accounts))
	for i, account := range accounts {
		out[i] = account
		active, done := 0, 0
		for _, target := range account.Targets {
			if target.Active {
				active++
				if target.State == "done" {
					done++
				}
			}
		}
		out[i].ActiveGroups, out[i].CompletedGroups = &active, &done
		out[i].Targets = []subTarget{}
	}
	return out
}
