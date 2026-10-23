package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/sync/errgroup"
)

type managementFreshProof struct {
	Stamp string
	Until time.Time
	Facts []managementGatewayFacts
}
type managementComputedPage struct {
	Proof managementFreshProof
	Page  managementPageResponse
	Bytes int
}
type managementComputedPages struct {
	mu    sync.Mutex
	pages map[string]managementComputedPage
	order []string
	bytes int
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
	query := `SELECT (SELECT jsonb_agg(jsonb_build_array(relation_name,revision) ORDER BY relation_name) FROM console_management_view_epoch),transaction_timestamp(),(SELECT min(deadline) FROM (SELECT deadline FROM console_configured_checks WHERE state='running' AND deadline>transaction_timestamp() UNION ALL SELECT deadline FROM console_model_fingerprints WHERE state='running' AND deadline>transaction_timestamp()) d), (SELECT COALESCE(jsonb_agg(to_jsonb(p) ORDER BY model),'[]'::jsonb) FROM ` + pgx.Identifier{s.engine.historyPath, "prices"}.Sanitize() + ` p)`
	if err = db.QueryRowContext(ctx, query).Scan(&epoch, &now, &until, &price); err != nil {
		return proof, now, err
	}
	proof.Stamp = tokenHash(mustJSON([]any{revision, string(epoch), string(price)}))
	if until.Valid {
		proof.Until = until.Time
	}
	return proof, now, nil
}

func (s *Service) rememberManagementPage(key string, proof *managementFreshProof, page managementPageResponse) {
	if proof == nil || proof.Stamp == "" {
		return
	}
	raw, err := json.Marshal(page)
	if err != nil || len(raw) > 8<<20 {
		return
	}
	// Decouple stored values from the builder's slice/pointer graph. Never
	// mutate the retained page; each read changes only its top-level timestamp.
	var immutable managementPageResponse
	if json.Unmarshal(raw, &immutable) != nil {
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
	for len(c.order) > 0 && (len(c.order) >= 4 || c.bytes+len(raw) > 16<<20) {
		old := c.order[0]
		c.order = c.order[1:]
		c.bytes -= c.pages[old].Bytes
		delete(c.pages, old)
	}
	minimal := *proof
	minimal.Facts = make([]managementGatewayFacts, len(proof.Facts))
	for i, f := range proof.Facts {
		minimal.Facts[i] = managementGatewayFacts{Source: f.Source, Revision: f.Revision, ConfigRevision: f.ConfigRevision, AtomicSnapshot: f.AtomicSnapshot}
	}
	c.pages[key] = managementComputedPage{Proof: minimal, Page: immutable, Bytes: len(raw)}
	c.order = append(c.order, key)
	c.bytes += len(raw)
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
	c := &s.managementComputed
	c.mu.Lock()
	entry, exists := c.pages[key]
	c.mu.Unlock()
	if !exists {
		return managementPageResponse{}, false, nil
	}
	sources, err := s.readManagementSources(ctx)
	if err != nil {
		return managementPageResponse{}, false, err
	}
	if verifyManagementSources(sources, entry.Proof.Facts) != nil {
		return managementPageResponse{}, false, nil
	}
	if err = verifyManagementFacts(ctx, entry.Proof.Facts); err != nil {
		if errors.Is(err, errManagementSourceRevision) {
			return managementPageResponse{}, false, nil
		}
		return managementPageResponse{}, false, err
	}
	proof, now, err := func() (managementFreshProof, time.Time, error) {
		tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			return managementFreshProof{}, time.Time{}, err
		}
		defer tx.Rollback()
		return s.managementReadProof(ctx, tx, owner)
	}()
	if err != nil {
		return managementPageResponse{}, false, err
	}
	if proof.Stamp == "" || proof.Stamp != entry.Proof.Stamp || !entry.Proof.Until.IsZero() && !now.Before(entry.Proof.Until) {
		return managementPageResponse{}, false, nil
	}
	if err = verifyManagementFacts(ctx, entry.Proof.Facts); err != nil {
		if errors.Is(err, errManagementSourceRevision) {
			return managementPageResponse{}, false, nil
		}
		return managementPageResponse{}, false, err
	}
	sources, err = s.readManagementSources(ctx)
	if err != nil {
		return managementPageResponse{}, false, err
	}
	if err = verifyManagementSources(sources, entry.Proof.Facts); err != nil {
		return managementPageResponse{}, false, err
	}
	if err = ctx.Err(); err != nil {
		return managementPageResponse{}, false, err
	}
	page := entry.Page
	page.ReadAtMS = now.UnixMilli()
	return page, true, nil
}
