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
	ctx = managementTraceContext(ctx)
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
			facts[i], err = readManagementGatewayWithParts(child, source)
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
	rememberManagementGatewayParts(ctx, facts)
	return view, nil
}

type managementQuerySnapshot struct {
	View     managementDatabaseView
	Page     managementQueryPage
	Rows     []managementQueryRow
	Evidence managementFilterEvidence
	Proof    *managementFreshProof
	// Request-local ordering already evaluated for Page. Never populated from a
	// retained page or reused to authorize a mutation.
	Filtered *managementFilteredRows
}

func (s *Service) readManagementQuerySnapshot(ctx context.Context, owner string, filters managementQueryFilters, token string, selection bool) (managementQuerySnapshot, error) {
	return s.readManagementQuerySnapshotMode(ctx, owner, filters, token, selection, false)
}

func (s *Service) readManagementQuerySnapshotMode(ctx context.Context, owner string, filters managementQueryFilters, token string, selection, rebase bool) (managementQuerySnapshot, error) {
	ctx = managementTraceContext(ctx)
	if !selection && owner != "" {
		ctx = context.WithValue(ctx, managementGatewayPartsScopeKey{}, &managementGatewayPartsScope{store: &s.gatewayParts, owner: owner})
	}
	out := managementQuerySnapshot{}
	view, err := s.withManagementSnapshot(ctx, func(tx *sql.Tx, facts []managementGatewayFacts) (managementDatabaseView, error) {
		ctx := ctx
		if !selection {
			directory := &managementDirectoryScope{store: &s.modelDirectories, predicates: &s.predicateSets, database: s.control.db, tx: tx, owner: owner}
			ctx = context.WithValue(ctx, managementDirectoryScopeKey{}, directory)
			proof, _, err := s.managementReadProof(ctx, tx, owner)
			if err != nil {
				return managementDatabaseView{}, err
			}
			proof.Facts = facts
			proof.CurrentExport = managementHasCompleteCurrentExport(facts)
			out.Proof = &proof
			ctx = context.WithValue(ctx, managementRateScopeKey{}, managementRateScopeFromProof(&s.rateSnapshots, s.control.db, tx, owner, proof))
			ctx = context.WithValue(ctx, managementQualityScopeKey{}, managementQualityScopeFromProof(&s.targetStreams, s.control.db, tx, owner, proof))
			ctx = context.WithValue(ctx, managementReceiptSetScopeKey{}, managementReceiptSetScopeFromProof(&s.receiptSets, s.control.db, tx, owner, proof))
			ctx = context.WithValue(ctx, managementPageModelScopeKey{}, &managementPageModelScope{store: &s.pageModelStreams, tx: tx, summaries: proof.Epochs["console_sub_model_summaries"]})
			directory.accountRevision = proof.Epochs["console_sub_accounts"]
			ctx = context.WithValue(ctx, managementTargetScopeKey{}, &managementTargetScope{
				store: &s.targetStreams, tx: tx,
				version: managementTargetVersion{s.control.db, owner, proof.Epochs["console_sub_accounts"], proof.Epochs["console_sub_targets"], proof.Epochs["console_sub_capability_summaries"]},
			})
		}
		catalogOnly := filters.Model == "" && filters.Availability == "" && filters.PriceStatus == "" && filters.ModelMatch == "" && filters.ToolUse == "" && filters.Quality == ""
		// withManagementSnapshot owns this repeatable-read transaction. Only
		// this path can retain predicate receipts until same-snapshot hydration.
		var reuse *managementReceiptReuse
		if !selection {
			reuse = &managementReceiptReuse{}
		}
		view, err := s.readManagementDatabaseModeWithReceiptsFrom(ctx, tx, owner, facts, !selection, filters.Model, catalogOnly && !selection, reuse)
		if err != nil {
			return view, err
		}
		endAssembly := managementReadSpan(ctx, "assemble_gateway")
		err = assembleManagementGatewayView(&view, facts, owner)
		endAssembly()
		if err != nil {
			return view, err
		}
		endRows := managementReadSpan(ctx, "assemble_rows")
		out.Rows, err = buildManagementQueryRows(view.Input, filters.Model, filters.AccountID)
		endRows()
		if err != nil {
			return view, err
		}
		endEvidence := managementReadSpan(ctx, "filter_evidence")
		out.Evidence = managementQueryEvidence(view.Input, out.Rows, filters.Model, view.Prices)
		endEvidence()
		endPage := managementReadSpan(ctx, "filter_page")
		filtered := filterManagementQueryRows(out.Rows, filters, out.Evidence)
		out.Filtered = &filtered
		out.Page, err = buildManagementQueryPageFromFilteredRowsMode(view.Input, filtered, filters, out.Evidence, owner, s.control.key, token, view.ReadAt, rebase && !selection)
		endPage()
		if err == nil && !selection {
			endHydrate := managementReadSpan(ctx, "hydrate_page")
			err = s.hydrateManagementPageFrom(ctx, tx, owner, &view, &out.Page, filters)
			endHydrate()
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
