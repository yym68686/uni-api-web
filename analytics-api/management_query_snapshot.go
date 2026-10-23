package main

import (
	"context"
	"database/sql"

	"golang.org/x/sync/errgroup"
)

func (s *Service) readManagementSources(ctx context.Context) ([]controlSource, error) {
	defer managementReadSpan(ctx, "sources")()
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return s.control.managementSourcesFrom(ctx, tx)
}

// Gateway revisions enclose the database snapshot. At the database read point,
// all gateway catalogs must still describe the same configuration generation.
// No database connection is held during network reads, no stale fallback is
// published, and a failed fence never triggers a mutation or an implicit retry.
func (s *Service) readManagementSnapshot(ctx context.Context, owner string) (managementDatabaseView, error) {
	return s.withManagementSnapshot(ctx, func(db *sql.Tx, facts []managementGatewayFacts) (managementDatabaseView, error) {
		view, err := s.readManagementDatabaseFrom(ctx, db, owner, facts)
		if err == nil {
			err = assembleManagementGatewayView(&view, facts, owner)
		}
		return view, err
	})
}

func (s *Service) withManagementSnapshot(ctx context.Context, read func(*sql.Tx, []managementGatewayFacts) (managementDatabaseView, error)) (managementDatabaseView, error) {
	sources, err := s.readManagementSources(ctx)
	if err != nil {
		return managementDatabaseView{}, err
	}
	facts := make([]managementGatewayFacts, len(sources))
	group, child := errgroup.WithContext(ctx)
	group.SetLimit(4)
	for i, source := range sources {
		group.Go(func() error {
			var err error
			facts[i], err = readManagementGateway(child, source)
			return err
		})
	}
	if err := group.Wait(); err != nil {
		return managementDatabaseView{}, err
	}
	view, err := func() (managementDatabaseView, error) {
		defer managementReadSpan(ctx, "database")()
		tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			return managementDatabaseView{}, err
		}
		defer tx.Rollback()
		return read(tx, facts)
	}()
	if err != nil {
		return managementDatabaseView{}, err
	}
	group, child = errgroup.WithContext(ctx)
	group.SetLimit(4)
	for _, source := range facts {
		group.Go(func() error { return verifyManagementGateway(child, source) })
	}
	if err := group.Wait(); err != nil {
		return managementDatabaseView{}, err
	}
	current, err := s.readManagementSources(ctx)
	if err != nil {
		return managementDatabaseView{}, err
	}
	if err := verifyManagementSources(current, facts); err != nil {
		return managementDatabaseView{}, err
	}
	return view, nil
}

type managementQuerySnapshot struct {
	View     managementDatabaseView
	Page     managementQueryPage
	Rows     []managementQueryRow
	Evidence managementFilterEvidence
}

func (s *Service) readManagementQuerySnapshot(ctx context.Context, owner string, filters managementQueryFilters, token string, selection bool) (managementQuerySnapshot, error) {
	out := managementQuerySnapshot{}
	view, err := s.withManagementSnapshot(ctx, func(tx *sql.Tx, facts []managementGatewayFacts) (managementDatabaseView, error) {
		view, err := s.readManagementDatabaseScopedFrom(ctx, tx, owner, facts, !selection, filters.Model)
		if err != nil {
			return view, err
		}
		if err = assembleManagementGatewayView(&view, facts, owner); err != nil {
			return view, err
		}
		out.Rows, err = buildManagementQueryRows(view.Input, filters.Model, filters.AccountID)
		if err != nil {
			return view, err
		}
		out.Evidence = managementQueryEvidence(view.Input, out.Rows, filters.Model, view.Prices)
		out.Page, err = buildManagementQueryPageFromRows(view.Input, out.Rows, filters, out.Evidence, owner, s.control.key, token, view.ReadAt)
		if err == nil && !selection {
			err = s.hydrateManagementPageFrom(ctx, tx, owner, &view, &out.Page, filters)
		}
		return view, err
	})
	if err != nil {
		// Never return a partial page when a post-read gateway/source fence fails.
		return managementQuerySnapshot{}, err
	}
	out.View = view
	return out, nil
}
