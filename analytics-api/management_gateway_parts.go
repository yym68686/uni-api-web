package main

import (
	"context"
	"encoding/json"
	"log"
	"sync"

	"golang.org/x/sync/errgroup"
)

const managementGatewayPartsBytes = 2 << 20

// Keep only configuration-versioned fields. Controls are deliberately absent:
// bootstrap_restore can change without increasing the gateway revision. Every
// reuse still reads complete CURRENT controls and independently authorizes the
// administrator with the scalar revision endpoint. No TTL or stale fallback.
type managementGatewayParts struct {
	Revision, ConfigRevision string
	Keys                     []managementGatewayKey
	Catalog                  []managementModel
	Routes                   map[string][]managementModel
	Providers                []configuredProvider
	Export                   map[string]any
}

type managementGatewayPartsEntry struct {
	owner, identity, revision, config string
	raw                               []byte
	bytes                             int
}

// Canonical private JSON gives every request an independent decoded copy,
// including provider credentials and exported definitions. Nothing here is
// serialized to a browser or log. At most two entries share a 2MiB ceiling.
type managementGatewayPartsStore struct {
	mu      sync.Mutex
	entries []managementGatewayPartsEntry
	bytes   int
}

type managementGatewayPartsScopeKey struct{}
type managementGatewayPartsScope struct {
	store *managementGatewayPartsStore
	owner string
}

func managementGatewayPartsScopeFrom(ctx context.Context) *managementGatewayPartsScope {
	scope, _ := ctx.Value(managementGatewayPartsScopeKey{}).(*managementGatewayPartsScope)
	if scope == nil || scope.store == nil || scope.owner == "" {
		return nil
	}
	return scope
}

func (s *managementGatewayPartsStore) get(owner, identity string) (managementGatewayPartsEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range s.entries {
		if entry.owner == owner && entry.identity == identity {
			return entry, true // raw is private immutable JSON; Unmarshal never edits it.
		}
	}
	return managementGatewayPartsEntry{}, false
}

func (s *managementGatewayPartsStore) put(entry managementGatewayPartsEntry) {
	if entry.bytes > managementGatewayPartsBytes {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	remove := func(i int) {
		s.bytes -= s.entries[i].bytes
		copy(s.entries[i:], s.entries[i+1:])
		s.entries[len(s.entries)-1] = managementGatewayPartsEntry{}
		s.entries = s.entries[:len(s.entries)-1]
	}
	for i, previous := range s.entries {
		if previous.owner == entry.owner && previous.identity == entry.identity {
			if previous.revision == entry.revision && previous.config == entry.config {
				return
			}
			remove(i)
			break
		}
	}
	for len(s.entries) > 0 && (len(s.entries) >= 2 || s.bytes+entry.bytes > managementGatewayPartsBytes) {
		remove(0)
	}
	s.entries = append(s.entries, entry)
	s.bytes += entry.bytes
}

// Called only after the complete database/gateway/source snapshot has passed
// all original fences. Selection/action/account readers never install scope.
func rememberManagementGatewayParts(ctx context.Context, facts []managementGatewayFacts) {
	scope := managementGatewayPartsScopeFrom(ctx)
	if scope == nil || ctx.Err() != nil {
		return
	}
	for _, facts := range facts {
		if !facts.AtomicSnapshot || facts.Source.configKeyError != nil || validateManagementGatewayFacts(facts) != nil {
			continue
		}
		identity := managementSourceIdentity(facts.Source)
		if previous, ok := scope.store.get(scope.owner, identity); ok && previous.revision == facts.Revision && previous.config == facts.ConfigRevision {
			continue
		}
		parts := managementGatewayParts{facts.Revision, facts.ConfigRevision, facts.Keys, facts.Catalog, facts.Routes, facts.Providers, facts.Export}
		raw, err := json.Marshal(parts)
		if err != nil || ctx.Err() != nil {
			continue
		}
		entry := managementGatewayPartsEntry{owner: scope.owner, identity: identity, revision: facts.Revision, config: facts.ConfigRevision, raw: raw}
		entry.bytes = len(raw) + len(entry.owner) + len(entry.identity) + len(entry.revision) + len(entry.config)
		scope.store.put(entry)
	}
}

func readManagementGatewayWithParts(ctx context.Context, source controlSource) (managementGatewayFacts, error) {
	scope := managementGatewayPartsScopeFrom(ctx)
	if scope == nil || source.configKeyError != nil {
		return readManagementGateway(ctx, source)
	}
	entry, ok := scope.store.get(scope.owner, managementSourceIdentity(source))
	if !ok {
		return readManagementGateway(ctx, source)
	}
	defer managementReadSpan(ctx, "gateway_parts_read")()
	admin := source
	if admin.ConfigKey != "" {
		admin.Key = admin.ConfigKey
	}
	var revision, controls map[string]any
	var code int
	var adminErr, observationErr error
	var group errgroup.Group
	group.Go(func() error {
		revision, code, adminErr = subGateway(ctx, admin, "GET", "/v1/channel-settings/management-revision", nil)
		return nil
	})
	group.Go(func() error {
		controls, _, observationErr = subGateway(ctx, source, "GET", "/v1/channel-controls", nil)
		return nil
	})
	_ = group.Wait()
	out := managementGatewayFacts{Source: source, AtomicSnapshot: true}
	if code == 404 && observationErr == nil {
		// An older replacement gateway cannot establish this cached version.
		// Negotiate only a genuine 404 using the original fresh-read protocol.
		return readManagementGateway(ctx, source)
	}
	if adminErr != nil {
		return out, adminErr
	}
	if revision["format"] != "management-revision-v1" {
		return out, errManagementSourceContract
	}
	current, _ := revision["revision"].(string)
	config, _ := revision["config_revision"].(string)
	if current == "" || config == "" {
		return out, errManagementSourceContract
	}
	if observationErr != nil {
		return out, observationErr
	}
	if controls["revision"] != current || controls["config_revision"] != config {
		return out, errManagementSourceRevision
	}
	if current != entry.revision || config != entry.config {
		// A newer agreed version is a cache miss, not a retry of a failed read.
		return readManagementGateway(ctx, source)
	}
	var parts managementGatewayParts
	if json.Unmarshal(entry.raw, &parts) != nil || ctx.Err() != nil {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		return out, errManagementSourceContract
	}
	out.Revision, out.ConfigRevision, out.Controls = parts.Revision, parts.ConfigRevision, controls
	out.Keys, out.Catalog, out.Routes, out.Providers, out.Export = parts.Keys, parts.Catalog, parts.Routes, parts.Providers, parts.Export
	if out.Revision != current || out.ConfigRevision != config {
		return out, errManagementSourceRevision
	}
	if err := validateManagementGatewayFacts(out); err != nil {
		return out, err
	}
	if managementTraceEnabled(ctx) {
		log.Printf("management_gateway_parts hit=true bytes=%d request_id=%s", entry.bytes, consoleRequestID(ctx))
	}
	return out, nil
}
