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
		return s.readManagementDatabaseFrom(ctx, tx, owner, facts)
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
	if err := assembleManagementGatewayView(&view, facts, owner); err != nil {
		return managementDatabaseView{}, err
	}
	return view, nil
}
