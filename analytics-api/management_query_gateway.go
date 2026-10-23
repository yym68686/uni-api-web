package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"time"

	"golang.org/x/sync/errgroup"
)

var errManagementSourceRevision = errors.New("gateway configuration changed during management read")
var errManagementSourceContract = errors.New("gateway cannot provide a complete versioned management read")

type managementGatewayKey struct {
	ID       string `json:"key_id"`
	Prefix   string `json:"prefix"`
	Position int    `json:"position"`
}

type managementGatewayFacts struct {
	Source                   controlSource `json:"-"`
	Controls                 map[string]any
	Revision, ConfigRevision string
	Keys                     []managementGatewayKey
	Catalog                  []managementModel
	Providers                []configuredProvider `json:"-"`
	Routes                   map[string][]managementModel
	Export                   map[string]any `json:"-"`
	AtomicSnapshot           bool           `json:"-"`
}

func managementSourceIdentity(source controlSource) string {
	return tokenHash(mustJSON([]string{source.ID, source.Name, source.Base, source.Key, source.ConfigKey}))
}

func managementCatalogRows(raw map[string]any, configRevision string) ([]managementModel, error) {
	if configRevision == "" || raw["snapshot_revision"] != configRevision {
		return nil, errManagementSourceRevision
	}
	if raw["data"] == nil {
		return nil, errManagementSourceContract
	}
	rows := []managementModel{}
	if err := decodeMap(raw["data"], &rows); err != nil {
		return nil, errManagementSourceContract
	}
	return rows, nil
}

// Bounded, read-only gateway configuration calls. A cached five-minute catalog
// cannot establish current configuration. The revision is checked again after
// the database snapshot is read, before any page is published.
func readManagementGateway(ctx context.Context, source controlSource) (managementGatewayFacts, error) {
	out, supported, err := readManagementGatewayAtomic(ctx, source)
	if supported || err != nil {
		return out, err
	}
	return readManagementGatewayLegacyWithControls(ctx, source, out.Controls)
}

func readManagementGatewayLegacy(ctx context.Context, source controlSource) (managementGatewayFacts, error) {
	return readManagementGatewayLegacyWithControls(ctx, source, nil)
}

func readManagementGatewayLegacyWithControls(ctx context.Context, source controlSource, controls map[string]any) (managementGatewayFacts, error) {
	out := managementGatewayFacts{Source: source, Routes: map[string][]managementModel{}}
	defer managementReadSpan(ctx, "gateway")()
	endControls := managementReadSpan(ctx, "gateway_controls")
	var err error
	if controls == nil {
		controls, _, err = subGateway(ctx, source, "GET", "/v1/channel-controls", nil)
	}
	endControls()
	if err != nil {
		return out, err
	}
	out.Controls = controls
	out.Revision, _ = controls["revision"].(string)
	out.ConfigRevision, _ = controls["config_revision"].(string)
	if out.Revision == "" || out.ConfigRevision == "" {
		return out, errManagementSourceContract
	}
	group, child := errgroup.WithContext(ctx)
	group.Go(func() error {
		defer managementReadSpan(child, "gateway_keys")()
		raw, _, err := fetchSource(child, source, "/v1/api-keys", nil)
		if err != nil {
			return err
		}
		if raw["snapshot_revision"] != out.ConfigRevision {
			return errManagementSourceRevision
		}
		if raw["data"] == nil {
			return errManagementSourceContract
		}
		if err := decodeMap(raw["data"], &out.Keys); err != nil {
			return errManagementSourceContract
		}
		seen := map[string]bool{}
		for _, key := range out.Keys {
			if key.ID == "" || seen[key.ID] {
				return errManagementSourceContract
			}
			seen[key.ID] = true
		}
		return nil
	})
	group.Go(func() error {
		defer managementReadSpan(child, "gateway_catalog")()
		raw, _, err := fetchSource(child, source, "/v1/model-channels", url.Values{"endpoint": {"all"}, "stream": {"all"}})
		if err != nil {
			return err
		}
		if raw["api_key_id"] != "" {
			return errManagementSourceContract
		}
		out.Catalog, err = managementCatalogRows(raw, out.ConfigRevision)
		return err
	})
	group.Go(func() error {
		defer managementReadSpan(child, "gateway_providers")()
		var err error
		out.Providers, err = effectiveProviders(child, source, true)
		return err
	})
	if controls["channel_settings"] == true && controls["temporary_channel_restore"] == true {
		group.Go(func() error {
			defer managementReadSpan(child, "gateway_export")()
			admin := source
			if source.ConfigKey != "" {
				admin.Key = source.ConfigKey
			}
			var err error
			out.Export, _, err = subGateway(child, admin, "GET", "/v1/channel-settings/export", nil)
			if err != nil {
				return err
			}
			if out.Export["revision"] != out.Revision {
				return errManagementSourceRevision
			}
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return out, err
	}
	providerNames := map[string]bool{}
	for _, provider := range out.Providers {
		if provider.Provider == "" || providerNames[provider.Provider] {
			return out, errManagementSourceContract
		}
		providerNames[provider.Provider] = true
	}
	for _, row := range out.Catalog {
		if row.Provider == "" || !providerNames[row.Provider] {
			return out, errManagementSourceContract
		}
	}
	parts := make([][]managementModel, len(out.Keys))
	group, child = errgroup.WithContext(ctx)
	group.SetLimit(4)
	for i, key := range out.Keys {
		group.Go(func() error {
			defer managementReadSpan(child, "gateway_key_catalog")()
			raw, _, err := fetchSource(child, source, "/v1/model-channels", url.Values{"api_key_id": {key.ID}, "endpoint": {"all"}, "stream": {"all"}})
			if err != nil {
				return err
			}
			if raw["api_key_id"] != key.ID {
				return errManagementSourceContract
			}
			parts[i], err = managementCatalogRows(raw, out.ConfigRevision)
			return err
		})
	}
	if err := group.Wait(); err != nil {
		return out, err
	}
	for i, key := range out.Keys {
		for _, row := range parts[i] {
			if row.Provider == "" || !providerNames[row.Provider] {
				return out, errManagementSourceContract
			}
		}
		out.Routes[key.ID] = parts[i]
	}
	if err := verifyManagementGateway(ctx, out); err != nil {
		return out, err
	}
	return out, nil
}

func verifyManagementGateway(ctx context.Context, facts managementGatewayFacts) error {
	defer managementReadSpan(ctx, "gateway_fence")()
	if facts.AtomicSnapshot {
		raw, _, err, controls, observationErr := managementGatewayDocuments(ctx, facts.Source, "/v1/channel-settings/management-revision")
		if err != nil {
			return err
		}
		if raw["format"] != "management-revision-v1" {
			return errManagementSourceContract
		}
		if raw["revision"] != facts.Revision || raw["config_revision"] != facts.ConfigRevision {
			return errManagementSourceRevision
		}
		if facts.Source.ConfigKey != "" && facts.Source.ConfigKey != facts.Source.Key {
			if observationErr != nil {
				return observationErr
			}
			if controls["revision"] != facts.Revision || controls["config_revision"] != facts.ConfigRevision {
				return errManagementSourceRevision
			}
		}
		return nil
	}
	controls, _, err := subGateway(ctx, facts.Source, "GET", "/v1/channel-controls", nil)
	if err != nil {
		return err
	}
	if controls["revision"] != facts.Revision || controls["config_revision"] != facts.ConfigRevision {
		return errManagementSourceRevision
	}
	return nil
}

func managementGatewayRoutes(facts managementGatewayFacts) managementQueryRoutes {
	consistent := true
	out := managementQueryRoutes{Data: []channelRoute{}, UnavailableKeys: []string{}, SnapshotConsistent: &consistent,
		Revision: facts.Revision, Manageable: facts.Controls["temporary_channel_management"] == true,
		BatchRevisions: true, AtomicBatch: true, OptimizeBatch: true, LegacyMigration: facts.Controls["automatic_engine"] == true}
	providers := map[string]bool{}
	for _, row := range facts.Catalog {
		providers[row.Provider] = true
	}
	for _, key := range facts.Keys {
		positions, seen := map[string]int{}, map[string]bool{}
		excluded := configuredExcluded(facts.Controls, key.ID)
		for _, row := range facts.Routes[key.ID] {
			if excluded[row.Provider] {
				continue
			}
			identity := mustJSON([]string{row.Provider, row.Model})
			if seen[identity] {
				continue
			}
			seen[identity] = true
			positions[row.Model]++
			providers[row.Provider] = true
			out.Data = append(out.Data, channelRoute{Provider: row.Provider, Model: row.Model, Upstream: row.Upstream, Engine: row.Engine, EngineMode: row.EngineMode, KeyID: key.ID, KeyPrefix: key.Prefix, KeyPosition: key.Position, Position: positions[row.Model]})
		}
	}
	origins := map[string]string{}
	// Sort before deriving names so collisions cannot depend on Go map order.
	names := []string{}
	for provider := range providers {
		names = append(names, provider)
	}
	sort.Strings(names)
	for _, provider := range names {
		for _, key := range facts.Keys {
			origins[configuredImportName(provider, key.ID)] = provider
		}
	}
	for i := range out.Data {
		out.Data[i].OriginProvider = origins[out.Data[i].Provider]
	}
	sort.SliceStable(out.Data, func(i, j int) bool {
		a, b := out.Data[i], out.Data[j]
		if a.KeyPosition != b.KeyPosition {
			return a.KeyPosition < b.KeyPosition
		}
		if a.Model != b.Model {
			return a.Model < b.Model
		}
		return a.Position < b.Position
	})
	return out
}

func managementSnapshotFromFacts(facts managementGatewayFacts) managementSnapshot {
	snapshot := managementSnapshot{Rows: facts.Catalog, Providers: map[string]managementProvider{}, Copies: map[string]bool{}, CheckedAt: time.Now().Unix(), Identity: controlTarget(facts.Source)}
	for _, provider := range facts.Providers {
		hashes, _ := json.Marshal(configuredKeyHashes(provider))
		snapshot.Providers[provider.Provider] = managementProvider{Base: subBindingSite(provider.Base), Fingerprint: tokenHash(controlTarget(facts.Source) + "\n" + provider.Provider + "\n" + subBindingSite(provider.Base) + "\n" + string(hashes)), ProbeFingerprint: configuredProbeFingerprint(facts.Source, provider)}
	}
	var controls subGatewayControls
	_ = decodeMap(facts.Controls, &controls)
	providers := map[string]bool{}
	for _, provider := range facts.Providers {
		providers[provider.Provider] = true
	}
	for provider := range providers {
		for _, channel := range controls.Temporary {
			if channel.Provider == configuredImportName(provider, channel.KeyID) {
				snapshot.Copies[channel.Provider] = true
			}
		}
	}
	return snapshot
}

func assembleManagementImports(facts managementGatewayFacts, refs []subChannelRef) ([]subInstalledChannel, error) {
	var controls subGatewayControls
	if err := decodeMap(facts.Controls, &controls); err != nil {
		return nil, errManagementSourceContract
	}
	mappings := map[string]map[string]string{}
	engines, modes := map[string]string{}, map[string]string{}
	for _, row := range facts.Catalog {
		engines[row.Provider], modes[row.Provider] = row.Engine, row.EngineMode
		if row.Upstream != "" && row.Upstream != row.Model {
			if mappings[row.Provider] == nil {
				mappings[row.Provider] = map[string]string{}
			}
			mappings[row.Provider][row.Model] = row.Upstream
		}
	}
	providerRefs := newSubProviderRefIndex(refs)
	providers := map[string]configuredProvider{}
	for _, provider := range facts.Providers {
		providers[provider.Provider] = provider
	}
	keys := map[string]managementGatewayKey{}
	for _, key := range facts.Keys {
		keys[key.ID] = key
	}
	type scope struct{ key, model string }
	rules := map[scope]map[string]int{}
	for _, rule := range controls.Rules {
		key := scope{rule.KeyID, rule.Model}
		if len(rule.Order) == 0 || rules[key] != nil {
			continue
		}
		positions := map[string]int{}
		for i, provider := range rule.Order {
			positions[provider] = i + 1
		}
		rules[key] = positions
	}
	out := []subInstalledChannel{}
	for _, channel := range controls.Temporary {
		provider, exists := providers[channel.Provider]
		if !exists || !provider.Temporary || provider.IdentityChanged || channel.IdentityChanged {
			continue
		}
		if _, exists := keys[channel.KeyID]; !exists {
			continue
		}
		matches := providerRefs.matches(channel.Provider, channel.KeyID, provider.Base)
		if len(matches) != 1 {
			continue
		}
		ref := matches[0]
		positions := map[string]int{}
		for _, model := range channel.Models {
			for _, key := range []scope{{channel.KeyID, model}, {channel.KeyID, ""}, {"", model}, {"", ""}} {
				if rule, exists := rules[key]; exists {
					if position, found := rule[channel.Provider]; found {
						positions[model] = position
					}
					break
				}
			}
		}
		key := keys[channel.KeyID]
		protocol := providerProtocol(channel.Provider)
		if protocol == "" {
			for _, spec := range channelProtocols {
				if spec.Engine == engines[channel.Provider] {
					protocol = spec.Protocol
				}
			}
		}
		out = append(out, subInstalledChannel{Protocol: protocol, Engine: engines[channel.Provider], EngineMode: modes[channel.Provider], ModelMappings: mappings[channel.Provider], Base: ref.Base, AccountID: ref.Account, GroupID: ref.Group, SourceID: facts.Source.ID, SourceName: facts.Source.Name, KeyID: channel.KeyID, KeyPosition: key.Position, KeyPrefix: key.Prefix, Provider: channel.Provider, Name: ref.Name, Models: channel.Models, Positions: positions, Revision: controls.Revision, Manageable: controls.Manageable})
	}
	return out, nil
}

func assembleManagementGatewayView(view *managementDatabaseView, facts []managementGatewayFacts, owner string) error {
	lookup := map[string]subInstalledChannel{}
	for _, binding := range view.Bindings {
		lookup[qualityChannelID(binding.SourceID, binding.Provider)] = binding
	}
	view.Input.Channels = []managementChannel{}
	view.Input.Imports = []subInstalledChannel{}
	view.Input.Routes = map[string]managementQueryRoutes{}
	view.Input.SourceRevisions = map[string]string{}
	available := map[string]bool{}
	for _, source := range facts {
		available[source.Source.ID] = true
		view.Input.SourceRevisions[source.Source.ID] = tokenHash(mustJSON([]string{managementSourceIdentity(source.Source), source.Revision, source.ConfigRevision}))
		snapshot := managementSnapshotFromFacts(source)
		view.Input.Channels = append(view.Input.Channels, assembleManagementChannels(source.Source, snapshot, lookup, view.BindingAccounts, owner)...)
		installed, err := assembleManagementImports(source, view.References)
		if err != nil {
			return err
		}
		view.Input.Imports = append(view.Input.Imports, installed...)
		routes := managementGatewayRoutes(source)
		for i := range routes.Data {
			routes.Data[i].EngineOrigin = view.EngineOrigins[source.Source.ID][routes.Data[i].Provider]
		}
		view.Input.Routes[source.Source.ID] = routes
	}
	seen := map[string]bool{}
	for _, channel := range view.Input.Imports {
		seen[qualityChannelID(channel.SourceID, channel.Provider)] = true
	}
	for _, binding := range view.Bindings {
		if !available[binding.SourceID] || seen[qualityChannelID(binding.SourceID, binding.Provider)] {
			continue
		}
		for _, source := range facts {
			if source.Source.ID == binding.SourceID {
				for _, row := range source.Catalog {
					if row.Provider == binding.Provider {
						binding.Engine, binding.EngineMode = row.Engine, row.EngineMode
					}
				}
			}
		}
		view.Input.Imports = append(view.Input.Imports, binding)
	}
	sort.Slice(view.Input.Imports, func(i, j int) bool {
		a, b := view.Input.Imports[i], view.Input.Imports[j]
		if a.SourceID != b.SourceID {
			return a.SourceID < b.SourceID
		}
		return a.KeyID+a.Provider < b.KeyID+b.Provider
	})
	sort.Slice(view.Input.Channels, func(i, j int) bool {
		return qualityChannelID(view.Input.Channels[i].SourceID, view.Input.Channels[i].Provider) < qualityChannelID(view.Input.Channels[j].SourceID, view.Input.Channels[j].Provider)
	})
	return nil
}

// One assembly implementation serves both the legacy inventory and the new
// version-fenced management query.
func assembleManagementChannels(src controlSource, snapshot managementSnapshot, lookup map[string]subInstalledChannel, accounts []subBindingAccount, owner string) []managementChannel {
	id := src.ID
	data := []managementChannel{}
	grouped := map[string]*managementChannel{}
	for _, row := range snapshot.Rows {
		if snapshot.Copies[row.Provider] {
			continue
		}
		item := grouped[row.Provider]
		if item == nil {
			binding, found := lookup[id+"\n"+row.Provider]
			if !found {
				binding = subInstalledChannel{Kind: "configured", SourceID: id, SourceName: src.Name, Provider: row.Provider, Name: row.Provider, Models: []string{}, Positions: map[string]int{}}
			}
			if live, ok := snapshot.Providers[row.Provider]; ok {
				// An edited credential must not inherit the previous account's
				// results while the durable binding worker catches up.
				if found && live.Fingerprint != binding.Fingerprint {
					binding.AccountID = ""
					binding.GroupID = 0
					binding.BoundKeys = nil
				}
				binding.Base = live.Base
			}
			binding.EngineMode = row.EngineMode
			item = &managementChannel{subInstalledChannel: binding, Engine: row.Engine, AccountIDs: []string{}}
			if live, ok := snapshot.Providers[row.Provider]; ok {
				item.ProbeFingerprint = live.ProbeFingerprint
			}
			ids := map[string]bool{}
			for _, key := range binding.BoundKeys {
				ids[key.AccountID] = true
			}
			// A site match enables navigation/filtering only. Probe results and
			// account-only data still require a verified key/group binding.
			if len(ids) == 0 && binding.Base != "" {
				for _, a := range accounts {
					if a.Owner == owner && subBindingSite(a.Base) == subBindingSite(binding.Base) {
						ids[a.ID] = true
					}
				}
			}
			for id := range ids {
				item.AccountIDs = append(item.AccountIDs, id)
			}
			sort.Strings(item.AccountIDs)
			grouped[row.Provider] = item
		}
		found := false
		for _, model := range item.Models {
			if model == row.Model {
				found = true
				break
			}
		}
		if !found {
			item.Models = append(item.Models, row.Model)
		}
		if row.Upstream != "" && row.Upstream != row.Model {
			if item.ModelMappings == nil {
				item.ModelMappings = map[string]string{}
			}
			item.ModelMappings[row.Model] = row.Upstream
		}
	}
	for _, item := range grouped {
		sort.Strings(item.Models)
		data = append(data, *item)
	}
	return data
}
