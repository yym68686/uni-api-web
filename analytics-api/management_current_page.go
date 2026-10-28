package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/sync/errgroup"
)

type managementFreshProof struct {
	Epochs          map[string]string
	Stamp           string
	ExportStamp     string
	CurrentExport   bool
	HeadersRevision int64
	Until           time.Time
	Facts           []managementGatewayFacts
	Accounts        []subAccount
}
type managementComputedPage struct {
	Proof managementFreshProof
	Page  managementPageResponse
	Bytes int
	Plan  []byte
}
type managementComputedPages struct {
	mu    sync.Mutex
	pages map[string]managementComputedPage
	order []string
	bytes int
}

func (c *managementComputedPages) get(key string) (managementComputedPage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, exists := c.pages[key]
	if exists {
		// A directory is used on every new page. Keep it recent while admitting
		// hydrated pages, so sequential navigation cannot evict its own plan.
		for i, k := range c.order {
			if k == key {
				c.order = append(append(c.order[:i], c.order[i+1:]...), key)
				break
			}
		}
	}
	return entry, exists
}

func managementComputedKey(owner string, f managementQueryFilters, cursor string) string {
	return tokenHash(owner + "\x00" + mustJSON(f) + "\x00" + cursor)
}

func (s *Service) managementReadProof(ctx context.Context, db controlQueryDB, owner string) (managementFreshProof, time.Time, error) {
	if pool, ok := db.(*sql.DB); ok {
		tx, err := pool.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			return managementFreshProof{}, time.Time{}, err
		}
		defer tx.Rollback()
		return s.managementReadProof(ctx, tx, owner)
	}
	proof := managementFreshProof{}
	revision, ready, err := readManagementCompactRevision(ctx, db, owner)
	if err != nil || !ready {
		return proof, time.Time{}, err
	}
	if s.engine == nil {
		return proof, time.Time{}, nil
	}
	var epoch []byte
	var price []byte
	var until sql.NullTime
	var now time.Time
	var backendPID int
	query := `SELECT (SELECT jsonb_agg(jsonb_build_array(relation_name,revision) ORDER BY relation_name) FROM console_management_view_epoch WHERE relation_name<>'console_sub_account_headers'),transaction_timestamp(),(SELECT min(deadline) FROM (SELECT deadline FROM console_configured_checks WHERE state='running' AND deadline>transaction_timestamp() UNION ALL SELECT deadline FROM console_model_fingerprints WHERE state='running' AND deadline>transaction_timestamp()) d), (SELECT revision FROM console_management_view_epoch WHERE relation_name='console_sub_account_headers'), (SELECT COALESCE(jsonb_agg(jsonb_build_array(to_jsonb(p),p.xmin::text) ORDER BY model),'[]'::jsonb) FROM ` + pgx.Identifier{s.engine.historyPath, "prices"}.Sanitize() + ` p),pg_backend_pid()`
	if err = db.QueryRowContext(ctx, query).Scan(&epoch, &now, &until, &proof.HeadersRevision, &price, &backendPID); err != nil {
		return proof, now, err
	}
	managementDatabaseSession(ctx, db, "proof", backendPID)
	var versions [][]json.RawMessage
	if err = json.Unmarshal(epoch, &versions); err != nil {
		return proof, now, err
	}
	proof.Epochs = map[string]string{}
	for _, v := range versions {
		if len(v) != 2 {
			return proof, now, errors.New("invalid management epoch")
		}
		var relation string
		if json.Unmarshal(v[0], &relation) != nil {
			return proof, now, errors.New("invalid management relation")
		}
		proof.Epochs[relation] = string(v[1])
	}
	// Engine-origin classification also depends on historical user overrides.
	// Include row generations so concurrent A->B->A edits cannot escape the fence.
	// No encrypted intent or credential payload is part of this proof.
	var overrides []byte
	if err = db.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_array(source_id,provider,id,row_version) ORDER BY source_id,provider,id,row_version),'[]'::jsonb)
 FROM (SELECT DISTINCT o.source_id,p->>'provider' AS provider,o.id,o.xmin::text AS row_version FROM console_channel_settings_operations o
 CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(o.public_result->'previews')='array' THEN o.public_result->'previews' ELSE '[]'::jsonb END) p
 WHERE o.status<>'rejected' AND p->>'provider' IS NOT NULL AND
 (p->>'created'='true' OR p->'before'->'engine' IS DISTINCT FROM p->'after'->'engine'
 OR p->'before'->'base_url' IS DISTINCT FROM p->'after'->'base_url')) origins`).Scan(&overrides); err != nil {
		return proof, now, err
	}
	proof.Stamp = tokenHash(mustJSON([]any{revision, string(epoch), string(price), string(overrides)}))
	exportedEpoch := make(map[string]string, len(proof.Epochs))
	for relation, value := range proof.Epochs {
		if relation != "console_control_snapshots" {
			exportedEpoch[relation] = value
		}
	}
	proof.ExportStamp = tokenHash(mustJSON([]any{revision, exportedEpoch, string(price), string(overrides)}))
	if until.Valid {
		proof.Until = until.Time
	}
	proof.Accounts, err = readSubAccountHeaders(ctx, db, owner, "")
	if err != nil {
		return managementFreshProof{}, now, err
	}
	return proof, now, nil
}

func (s *Service) rememberManagementPage(key string, proof *managementFreshProof, page managementPageResponse) {
	if proof == nil || proof.Stamp == "" {
		return
	}
	// Persist one complete lossless representation. JSON roundtrip discards
	// the wire structs' shadowed legacy fields, which otherwise retain the
	// entire expanded result graph in addition to the columns. Admission must
	// measure this retained representation: expanded rows repeat each model's
	// evidence in targets/checks/selection and can exceed the budget while the
	// canonical columns fit comfortably inside it.
	columns, err := encodeManagementColumnPage(page)
	if err != nil {
		return
	}
	encodedColumns, err := json.Marshal(columns)
	if err != nil {
		return
	}
	storedBytes := len(encodedColumns)
	if storedBytes > 16<<20 {
		log.Printf("management_computation_store skipped=true reason=wire_size bytes=%d", storedBytes)
		return
	}
	var immutable managementColumnPage
	if json.Unmarshal(encodedColumns, &immutable) != nil {
		return
	}
	compact := immutable.managementPageResponse
	compact.Format = "management-page-v1"
	compact.Rows = make([]managementQueryRow, len(immutable.Rows))
	for i, row := range immutable.Rows {
		compact.Rows[i] = row.managementQueryRow
		compact.Rows[i].Target = row.Target.subTarget
	}
	compact.preparedColumns = &immutable
	s.rememberManagementEntry(key, proof, managementComputedPage{Page: compact, Bytes: storedBytes})
}

func (s *Service) rememberManagementEntry(key string, proof *managementFreshProof, entry managementComputedPage) {
	if proof == nil || proof.Stamp == "" || entry.Bytes > 16<<20 {
		return
	}
	c := &s.managementComputed
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pages == nil {
		c.pages = map[string]managementComputedPage{}
	}
	if old, ok := c.pages[key]; ok {
		c.bytes -= old.Bytes
		delete(c.pages, key)
		for i, k := range c.order {
			if k == key {
				c.order = append(c.order[:i], c.order[i+1:]...)
				break
			}
		}
	}
	for len(c.order) > 0 && (len(c.order) >= 4 || c.bytes+entry.Bytes > 16<<20) {
		old := c.order[0]
		c.order = c.order[1:]
		c.bytes -= c.pages[old].Bytes
		delete(c.pages, old)
	}
	minimal := *proof
	if minimal.CurrentExport {
		minimal.Stamp = minimal.ExportStamp
	}
	minimal.Accounts = nil
	minimal.Facts = make([]managementGatewayFacts, len(proof.Facts))
	for i, f := range proof.Facts {
		minimal.Facts[i] = managementGatewayFacts{Source: f.Source, Revision: f.Revision, ConfigRevision: f.ConfigRevision, AtomicSnapshot: f.AtomicSnapshot}
	}
	entry.Proof = minimal
	c.pages[key] = entry
	c.order = append(c.order, key)
	c.bytes += entry.Bytes
}

func verifyManagementFacts(ctx context.Context, facts []managementGatewayFacts) error {
	g, child := errgroup.WithContext(ctx)
	g.SetLimit(4)
	for _, f := range facts {
		g.Go(func() error { return verifyManagementGateway(child, f) })
	}
	return g.Wait()
}

// Reuse a computation only after live authorization and revision checks prove
// it describes this request's CURRENT PostgreSQL snapshot and gateway config.
// No stale result is published while validation/recomputation is pending.
func (s *Service) currentComputedManagementPage(ctx context.Context, owner, key string) (managementPageResponse, bool, error) {
	return s.currentComputedManagementPageMode(ctx, owner, key, false)
}
func (s *Service) currentComputedManagementPageMode(ctx context.Context, owner, key string, compact bool) (managementPageResponse, bool, error) {
	ctx = managementTraceContext(ctx)
	entry, exists := s.managementComputed.get(key)
	if !exists {
		log.Printf("management_current_miss reason=absent request_id=%s", consoleRequestID(ctx))
		return managementPageResponse{}, false, nil
	}
	proof, now, valid, err := s.currentManagementProof(ctx, owner, entry.Proof)
	if err != nil || !valid {
		return managementPageResponse{}, false, err
	}
	page := entry.Page
	page.ReadAtMS = now.UnixMilli()
	current := map[string]subAccount{}
	for _, a := range proof.Accounts {
		current[a.ID] = a
	}
	page.Accounts = append([]subAccount{}, entry.Page.Accounts...)
	for i, a := range page.Accounts {
		fresh, ok := current[a.ID]
		if !ok {
			return managementPageResponse{}, false, nil
		}
		fresh.ActiveGroups, fresh.CompletedGroups = a.ActiveGroups, a.CompletedGroups
		page.Accounts[i] = fresh
	}
	page.Rows = append([]managementQueryRow{}, entry.Page.Rows...)
	for i := range page.Rows {
		if fresh, ok := current[page.Rows[i].Account.ID]; ok {
			page.Rows[i].Account = fresh
		}
	}
	if !compact {
		wire, err := currentManagementColumnPage(page)
		if err != nil {
			return managementPageResponse{}, false, err
		}
		full, err := expandManagementColumnPage(wire)
		if err != nil {
			return managementPageResponse{}, false, err
		}
		return full, true, nil
	}
	return page, true, nil
}

func (s *Service) currentManagementProof(ctx context.Context, owner string, expected managementFreshProof) (managementFreshProof, time.Time, bool, error) {
	sources, err := s.readManagementSources(ctx)
	if err != nil {
		return managementFreshProof{}, time.Time{}, false, err
	}
	if verifyManagementSources(sources, expected.Facts) != nil {
		return managementFreshProof{}, time.Time{}, false, nil
	}
	// The current proof is cheap and monotonic. Sandwich one live gateway
	// authorization/revision read between two PostgreSQL proofs instead of
	// performing two remote round trips around one database snapshot. Equal
	// proofs establish that the complete stored page was current at the live
	// gateway read. Headers are compared too, even though balance changes do
	// not invalidate the retained channel computation between requests.
	readProof := func() (managementFreshProof, time.Time, error) {
		defer managementReadSpan(ctx, "current_database_proof")()
		return s.managementReadProof(ctx, s.control.db, owner)
	}
	proof, now, err := readProof()
	if err != nil {
		return managementFreshProof{}, time.Time{}, false, err
	}
	valid := func(p managementFreshProof, at time.Time) bool {
		return managementProofStamp(p, expected.CurrentExport) != "" && managementProofStamp(p, expected.CurrentExport) == expected.Stamp &&
			(expected.Until.IsZero() || at.Before(expected.Until))
	}
	if !valid(proof, now) {
		changed := []string{}
		for relation, value := range proof.Epochs {
			if expected.CurrentExport && relation == "console_control_snapshots" {
				continue
			}
			if expected.Epochs[relation] != value && strings.HasPrefix(relation, "console_") && len(relation) < 80 {
				changed = append(changed, relation)
			}
		}
		sort.Strings(changed)
		log.Printf("management_current_miss reason=proof relations=%s request_id=%s", strings.Join(changed, ","), consoleRequestID(ctx))
		return managementFreshProof{}, time.Time{}, false, nil
	}
	if err = verifyManagementFacts(ctx, expected.Facts); err != nil {
		if errors.Is(err, errManagementSourceRevision) {
			return managementFreshProof{}, time.Time{}, false, nil
		}
		return managementFreshProof{}, time.Time{}, false, err
	}
	after, afterAt, err := readProof()
	if err != nil {
		return managementFreshProof{}, time.Time{}, false, err
	}
	if !valid(after, afterAt) || !proof.Until.Equal(after.Until) || proof.HeadersRevision != after.HeadersRevision || mustJSON(proof.Accounts) != mustJSON(after.Accounts) {
		log.Printf("management_current_miss reason=concurrent_change request_id=%s", consoleRequestID(ctx))
		return managementFreshProof{}, time.Time{}, false, nil
	}
	sources, err = s.readManagementSources(ctx)
	if err != nil {
		return managementFreshProof{}, time.Time{}, false, err
	}
	if err = verifyManagementSources(sources, expected.Facts); err != nil {
		return managementFreshProof{}, time.Time{}, false, err
	}
	if err = ctx.Err(); err != nil {
		return managementFreshProof{}, time.Time{}, false, err
	}
	proof.CurrentExport = expected.CurrentExport
	proof.Stamp = managementProofStamp(proof, expected.CurrentExport)
	return proof, now, true, nil
}

func managementProofStamp(proof managementFreshProof, currentExport bool) string {
	if currentExport {
		return proof.ExportStamp
	}
	return proof.Stamp
}

// Only complete CURRENT gateway definitions eliminate the saved-snapshot
// fallback. A missing definition keeps the original epoch fence intact.
func managementHasCompleteCurrentExport(facts []managementGatewayFacts) bool {
	for _, f := range facts {
		var definitions map[string]json.RawMessage
		var controls subGatewayControls
		if f.Export == nil || decodeMap(f.Export["temporary_definitions"], &definitions) != nil || definitions == nil || decodeMap(f.Controls, &controls) != nil {
			return false
		}
		for _, channel := range controls.Temporary {
			if _, ok := definitions[channel.Provider]; !ok {
				return false
			}
		}
	}
	return true
}
