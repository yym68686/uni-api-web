package main

import (
	"bytes"
	"encoding/json"
	"slices"
)

type managementFingerprintRecord struct {
	fingerprintRecord
	UpdatedAt *int64 `json:"updated_at_ms,omitempty"`
}

type managementQueryRoutes struct {
	Data               []channelRoute `json:"data"`
	UnavailableKeys    []string       `json:"unavailable_keys"`
	SnapshotConsistent *bool          `json:"snapshot_consistent,omitempty"`
	Revision           string         `json:"revision,omitempty"`
	Manageable         bool           `json:"manageable,omitempty"`
	BatchRevisions     bool           `json:"batch_revisions,omitempty"`
	AtomicBatch        bool           `json:"atomic_batch,omitempty"`
	OptimizeBatch      bool           `json:"optimize_batch,omitempty"`
	LegacyMigration    bool           `json:"legacy_engine_migration,omitempty"`
}

var managementFingerprintModels = func() []string {
	decoder := json.NewDecoder(bytes.NewReader(fingerprintModelsJSON))
	if _, err := decoder.Token(); err != nil {
		panic(err)
	}
	models := []string{}
	for decoder.More() {
		name, err := decoder.Token()
		if err != nil {
			panic(err)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			panic(err)
		}
		models = append(models, name.(string))
	}
	return models
}()

func managementFingerprintKey(t fingerprintTarget) string {
	return mustJSON([]any{t.Source, t.Provider, t.Account, t.Group, t.Model})
}

func managementFingerprintAt(record managementFingerprintRecord) int64 {
	if record.UpdatedAt != nil {
		return *record.UpdatedAt
	}
	return managementRawAt(record.Result) * 1000
}

func managementSharedFingerprints(in managementQueryInput) map[string]managementFingerprintRecord {
	index := map[string]managementFingerprintRecord{}
	for _, record := range in.Fingerprints {
		index[managementFingerprintKey(record.fingerprintTarget)] = record
	}
	imports := map[string][]subInstalledChannel{}
	for _, channel := range in.Imports {
		if channel.Kind == "configured" || slices.Contains(in.UnavailableImports, channel.SourceID) || slices.Contains(in.UnavailableImports, channel.SourceName) {
			continue
		}
		id := qualityChannelID(channel.SourceID, channel.Provider)
		imports[id] = append(imports[id], channel)
	}
	type group struct {
		keys    map[string]bool
		records []managementFingerprintRecord
	}
	groups := map[string]*group{}
	groupOrder := []string{}
	for _, member := range in.Channels {
		if member.ProbeFingerprint == "" || slices.Contains(in.UnavailableSources, member.SourceID) || slices.Contains(in.UnavailableSources, member.SourceName) {
			continue
		}
		copies := imports[qualityChannelID(member.SourceID, member.Provider)]
		identities := map[string]bool{}
		for _, copy := range copies {
			identities[qualityGroupID(copy.AccountID, copy.GroupID)] = true
		}
		if len(identities) > 1 {
			continue
		}
		selected := member
		if len(copies) > 0 {
			selected.subInstalledChannel = copies[0]
		}
		bound := managementBoundGroups(selected)
		unique := []subBoundKey{}
		seen := map[string]bool{}
		for _, key := range bound {
			id := qualityGroupID(key.AccountID, key.GroupID)
			if key.AccountID != "" && key.GroupID > 0 && !seen[id] {
				seen[id] = true
				unique = append(unique, key)
			}
		}
		if len(unique) != 1 {
			continue
		}
		binding := unique[0]
		for _, model := range member.Models {
			if managementUpstream(member, model) != model || len(copies) > 0 && (!slices.Contains(selected.Models, model) || managementUpstream(selected, model) != model) {
				continue
			}
			sourceKey := managementFingerprintKey(fingerprintTarget{Source: member.SourceID, Provider: member.Provider, Model: model})
			groupKey := managementFingerprintKey(fingerprintTarget{Account: binding.AccountID, Group: binding.GroupID, Model: model})
			g := groups[groupKey]
			if g == nil {
				g = &group{keys: map[string]bool{groupKey: true}}
				if site, exists := index[groupKey]; exists {
					g.records = append(g.records, site)
				}
				groups[groupKey] = g
				groupOrder = append(groupOrder, groupKey)
			}
			g.keys[sourceKey] = true
			if record, exists := index[sourceKey]; exists {
				if record.State != "done" || record.Revision == member.ProbeFingerprint {
					g.records = append(g.records, record)
				} else {
					delete(index, sourceKey)
				}
			}
		}
	}
	for _, key := range groupOrder {
		g := groups[key]
		if len(g.records) == 0 {
			continue
		}
		latest := g.records[0]
		for _, record := range g.records[1:] {
			if managementPending(record.State) && !managementPending(latest.State) || managementPending(record.State) == managementPending(latest.State) && managementFingerprintAt(record) > managementFingerprintAt(latest) {
				latest = record
			}
		}
		for key := range g.keys {
			index[key] = latest
		}
	}
	return index
}

type managementFingerprintEntry struct {
	Target fingerprintTarget            `json:"target"`
	Record *managementFingerprintRecord `json:"record,omitempty"`
}

func managementRowFingerprints(row managementQueryRow, index map[string]managementFingerprintRecord, model string) []string {
	statuses := []string{}
	for _, entry := range managementRowFingerprintEntries(row, index, model) {
		status := "untested"
		if record := entry.Record; record != nil {
			status = record.State
			if status == "done" {
				var result struct {
					Status string `json:"status"`
				}
				_ = json.Unmarshal(record.Result, &result)
				status = result.Status
				if status == "" {
					status = "error"
				}
			}
		}
		statuses = append(statuses, status)
	}
	return statuses
}

func managementRowFingerprintEntries(row managementQueryRow, index map[string]managementFingerprintRecord, model string) []managementFingerprintEntry {
	models := managementFingerprintModels
	if model != "" {
		models = []string{model}
	}
	entries := []managementFingerprintEntry{}
	for _, name := range models {
		if !fingerprintModel(name) {
			continue
		}
		target := fingerprintTarget{Model: name}
		if row.Account.ID != "" {
			target.Account, target.Group = row.Account.ID, row.Target.GroupID
		} else if row.Configured != nil && len(row.Configured.Members) > 0 {
			member := row.Configured.Members[0]
			for _, candidate := range row.Configured.Members {
				if slices.Contains(candidate.Models, name) {
					member = candidate
					break
				}
			}
			target.Source, target.Provider = member.SourceID, member.Provider
		} else {
			continue
		}
		key := managementFingerprintKey(target)
		record, exists := index[key]
		if exists && record.State == "done" && target.Source != "" && managementFingerprintKey(record.fingerprintTarget) == key && row.Configured != nil {
			for _, member := range row.Configured.Members {
				if member.SourceID == target.Source && member.Provider == target.Provider && member.ProbeFingerprint != record.Revision {
					exists = false
				}
			}
		}
		entry := managementFingerprintEntry{Target: target}
		if exists {
			entry.Record = &record
		}
		entries = append(entries, entry)
	}
	return entries
}

func managementRowJoined(row managementQueryRow, in managementQueryInput) string {
	if in.Imports == nil {
		return "unknown"
	}
	members := []managementChannel{}
	if row.Configured != nil {
		members = row.Configured.Members
	} else {
		for _, channel := range in.Imports {
			member := managementChannel{subInstalledChannel: channel}
			for _, group := range managementBoundGroups(member) {
				if group.AccountID == row.Account.ID && group.GroupID == row.Target.GroupID {
					members = append(members, member)
					break
				}
			}
		}
	}
	for _, member := range members {
		if member.Kind != "configured" && member.KeyID != "" && len(member.Models) > 0 {
			return "joined"
		}
	}
	unknown := len(in.UnavailableImports) > 0
	for _, member := range members {
		routes, exists := in.Routes[member.SourceID]
		for _, route := range routes.Data {
			if route.KeyID != "" && (route.Provider == member.Provider || route.OriginProvider == member.Provider) {
				return "joined"
			}
		}
		unknown = unknown || !exists || len(routes.UnavailableKeys) > 0 || routes.SnapshotConsistent != nil && !*routes.SnapshotConsistent
	}
	if unknown {
		return "unknown"
	}
	return "unjoined"
}

func managementQueryEvidence(in managementQueryInput, rows []managementQueryRow, model string, prices []Price) managementFilterEvidence {
	index := managementSharedFingerprints(in)
	evidence := managementFilterEvidence{Prices: prices, FingerprintReady: in.FingerprintReady, Fingerprints: map[string][]string{}, Joined: map[string]string{}}
	for _, row := range rows {
		evidence.Fingerprints[row.ID] = managementRowFingerprints(row, index, model)
		evidence.Joined[row.ID] = managementRowJoined(row, in)
	}
	return evidence
}
