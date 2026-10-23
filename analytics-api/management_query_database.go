package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type managementDatabaseView struct {
	Input           managementQueryInput
	Prices          []Price
	Bindings        []subInstalledChannel
	References      []subChannelRef
	BindingAccounts []subBindingAccount
	Sources         []controlSource `json:"-"`
	ReadAt          time.Time
	EngineOrigins   map[string]map[string]string
	ProjectedModels bool
}

// Caller owns one repeatable-read transaction for the complete view. Sources
// are returned privately for live revision fencing; their credentials are
// never part of a page or cursor. No historical request partitions are scanned.
func (s *Service) readManagementDatabaseFrom(ctx context.Context, db controlQueryDB, owner string, facts []managementGatewayFacts) (managementDatabaseView, error) {
	return s.readManagementDatabaseViewFrom(ctx, db, owner, facts, false)
}

func (s *Service) readManagementDatabaseViewFrom(ctx context.Context, db controlQueryDB, owner string, facts []managementGatewayFacts, predicates bool) (managementDatabaseView, error) {
	return s.readManagementDatabaseScopedFrom(ctx, db, owner, facts, predicates, "")
}

func (s *Service) readManagementDatabaseScopedFrom(ctx context.Context, db controlQueryDB, owner string, facts []managementGatewayFacts, predicates bool, model string) (managementDatabaseView, error) {
	return s.readManagementDatabaseModeFrom(ctx, db, owner, facts, predicates, model, false)
}

func (s *Service) readManagementDatabaseModeFrom(ctx context.Context, db controlQueryDB, owner string, facts []managementGatewayFacts, predicates bool, model string, catalogOnly bool) (managementDatabaseView, error) {
	ctx = managementTraceContext(ctx)
	out := managementDatabaseView{}
	trace := newManagementReadTimeline(ctx, "database_sources")
	defer trace.finish()
	if err := db.QueryRowContext(ctx, `SELECT transaction_timestamp()`).Scan(&out.ReadAt); err != nil {
		return out, err
	}
	var err error
	out.Sources, err = s.control.managementSourcesFrom(ctx, db)
	if err != nil {
		return out, err
	}
	if err := verifyManagementSources(out.Sources, facts); err != nil {
		return out, err
	}
	trace.next("database_bindings")
	out.Bindings, err = s.managementBindingsFrom(ctx, db, owner, facts)
	if err != nil {
		return out, err
	}
	out.References, err = s.control.subChannelRefsForAccountFrom(ctx, db, owner, "")
	if err != nil {
		return out, err
	}
	out.BindingAccounts, err = s.bindingAccountsFrom(ctx, db)
	if err != nil {
		return out, err
	}
	out.EngineOrigins = map[string]map[string]string{}
	for _, source := range facts {
		origins, err := s.managementEngineOriginsFrom(ctx, db, source, out.References)
		if err != nil {
			return out, err
		}
		out.EngineOrigins[source.Source.ID] = origins
	}
	trace.next("database_quality")
	bindings, err := managementQualityBindings(out.Bindings, out.References, facts)
	if err != nil {
		return out, err
	}
	shared, err := readSharedQualityProjectionFrom(ctx, db, owner, "", bindings)
	if err != nil {
		return out, err
	}
	trace.next("database_account_headers")
	out.Input.Accounts, err = readSubAccountHeaders(ctx, db, owner, "")
	if err != nil {
		return out, err
	}
	trace.next("database_account_details")
	var signals []managementModelSignals
	if predicates {
		endPredicates := managementReadSpan(ctx, "database_predicate_snapshot")
		if catalogOnly {
			out.Input.ModelsRevision, signals, err = readManagementRevisionCatalogFrom(ctx, db, owner, true)
		} else if model != "" {
			signals, out.Input.ModelsRevision, err = readManagementScopedSignals(ctx, db, owner, model, managementScopeModelNames(model, facts, out.Bindings))
		} else {
			signals, out.Input.ModelsRevision, err = readCurrentManagementSignalsAndRevision(ctx, db, owner)
		}
		endPredicates()
	}
	if !predicates || errors.Is(err, errManagementPredicateFallback) {
		out.Input.ModelsRevision, err = readManagementModelRevisionFrom(ctx, db, owner)
	}
	if errors.Is(err, errManagementPredicateFallback) {
		out.Input.ModelsRevision, err = "", nil
	}
	if err != nil {
		return out, err
	}
	var readModels func([]subAccount) error
	if signals != nil {
		readModels = func(accounts []subAccount) error {
			index := map[string]*subTarget{}
			for i := range accounts {
				for j := range accounts[i].Targets {
					target := &accounts[i].Targets[j]
					index[qualityGroupID(accounts[i].ID, target.GroupID)] = target
				}
			}
			for _, item := range signals {
				if target := index[qualityGroupID(item.Account, item.Group)]; target != nil {
					target.Models = append(target.Models, item.Model)
				}
			}
			out.ProjectedModels = true
			return nil
		}
	}
	var readRates func() (map[string]map[string]subObservedRate, error)
	if out.Input.ModelsRevision != "" {
		readRates = func() (map[string]map[string]subObservedRate, error) {
			return s.control.subObservedRatesPreparedFrom(ctx, db, owner, "")
		}
	}
	if err = s.readSubAccountDetailsWithModelsFrom(ctx, db, owner, "", 0, "", true, false, out.Input.Accounts, nil, shared, readModels, readRates, true); err != nil {
		return out, err
	}
	trace.next("database_normalize")
	// Filter the same summary representation the browser currently receives.
	// Private probe IDs are needed for receipt attachment, then discarded before
	// shared-quality selection and any page serialization.
	if err := normalizeManagementAccounts(out.Input.Accounts); err != nil {
		return out, err
	}
	trace.next("database_configured_checks")
	out.Input.Checks, err = readConfiguredChecksFrom(ctx, db, "", "", "")
	if err != nil {
		return out, err
	}
	for i, check := range out.Input.Checks {
		raw, err := json.Marshal(compactSummary(check))
		if err != nil {
			return out, err
		}
		if err := json.Unmarshal(raw, &out.Input.Checks[i]); err != nil {
			return out, err
		}
		out.Input.Checks[i].DetailsOmitted = true
	}
	trace.next("database_fingerprints")
	fingerprints, err := readManagementFingerprintsFrom(ctx, db, owner)
	if err != nil {
		return out, err
	}
	out.Input.Fingerprints = []managementFingerprintRecord{}
	for _, record := range fingerprints {
		at := record.UpdatedAtMS
		out.Input.Fingerprints = append(out.Input.Fingerprints, managementFingerprintRecord{fingerprintRecord: record, UpdatedAt: &at})
	}
	out.Input.FingerprintReady = true
	if s.engine == nil {
		return out, errors.New("management pricing unavailable")
	}
	trace.next("database_prices")
	table := pgx.Identifier{s.engine.historyPath, "prices"}.Sanitize()
	rows, err := db.QueryContext(ctx, `SELECT model,input,output,cache_read,cache_write,cache_write_1h,source,verified,effective_at,charge_cache_write,sale_percent,long_context_premium FROM `+table+` ORDER BY model`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	prices := []Price{}
	for rows.Next() {
		var price Price
		if err := rows.Scan(&price.Model, &price.Input, &price.Output, &price.CacheRead, &price.CacheWrite, &price.CacheWrite1h, &price.Source, &price.Verified, &price.EffectiveAt, &price.ChargeCacheWrite, &price.SalePercent, &price.LongContextPremium); err != nil {
			return out, err
		}
		prices = append(prices, price)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	out.Prices = withCatalogPrices(prices)
	return out, nil
}

func normalizeManagementAccounts(accounts []subAccount) error {
	for i := range accounts {
		for j := range accounts[i].Targets {
			target := &accounts[i].Targets[j]
			models := target.Models
			target.Models = nil
			raw, err := json.Marshal(compactSummary(*target))
			if err != nil {
				return err
			}
			*target = subTarget{}
			if err := json.Unmarshal(raw, target); err != nil {
				return err
			}
			for k := range models {
				model := &models[k]
				model.Message = summaryMessage(model.Message)
				compactSubModelResult(model.Result)
				if model.Result != nil {
					model.Result.Availability.ID, model.Result.Quality.ID = "", ""
					model.Result.Availability.Message = summaryMessage(model.Result.Availability.Message)
					model.Result.Quality.Message = summaryMessage(model.Result.Quality.Message)
				}
			}
			target.Models = models
			target.DetailsOmitted = true
		}
	}
	return nil
}

func readConfiguredChecksFrom(ctx context.Context, db controlQueryDB, sourceID, provider, model string) ([]configuredCheck, error) {
	rows, err := db.QueryContext(ctx, `SELECT c.source_id,c.provider,c.kind,c.model,c.fingerprint,
 CASE WHEN c.state='running' AND c.deadline<now() THEN 'interrupted' ELSE c.state END,c.message,c.result,
 COALESCE(h.total,0),COALESCE(h.successful,0),COALESCE(h.passed,0)
 FROM console_configured_checks c JOIN console_sources s ON s.id=c.source_id
 LEFT JOIN console_quality_totals h ON h.source_id=c.source_id AND h.provider=c.provider WHERE s.enabled
 AND ($1='' OR c.source_id=$1) AND ($2='' OR c.provider=$2)
 AND ($3='' OR c.model=$3 OR c.model='' OR c.model='gpt-6-astra')`, sourceID, provider, model)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	data := []configuredCheck{}
	for rows.Next() {
		var c configuredCheck
		var raw []byte
		if err = rows.Scan(&c.Source, &c.Provider, &c.Kind, &c.Model, &c.Fingerprint, &c.State, &c.Message, &raw, &c.History.Total, &c.History.Successful, &c.History.Passed); err != nil {
			break
		}
		c.Result = raw
		data = append(data, c)
	}
	if err == nil {
		err = rows.Err()
	}
	if err != nil {
		return nil, err
	}

	return data, nil
}

func readManagementFingerprintsFrom(ctx context.Context, db controlQueryDB, owner string) ([]fingerprintRecord, error) {
	rows, err := db.QueryContext(ctx, `SELECT COALESCE(f.source_id,''),f.provider,COALESCE(f.account_id,''),f.group_id,f.model,
 CASE WHEN f.state='running' AND f.deadline<now() THEN 'interrupted' ELSE f.state END,f.message,f.revision,f.result,
 COALESCE(a.base,''),COALESCE(t.encrypted_key,''),floor(extract(epoch FROM f.updated_at)*1000)::BIGINT
 FROM console_model_fingerprints f LEFT JOIN console_sub_accounts a ON a.id=f.account_id
 LEFT JOIN console_sub_targets t ON t.account_id=f.account_id AND t.group_id=f.group_id
 LEFT JOIN console_sources s ON s.id=f.source_id WHERE (a.owner=$1 OR s.enabled)`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	data := []fingerprintRecord{}
	for rows.Next() {
		var f fingerprintRecord
		var base, key string
		// Queued, running and unsuccessful checks legitimately have SQL NULL
		// results. database/sql supports NULL for []byte, but not *json.RawMessage.
		var result []byte
		if err = rows.Scan(&f.Source, &f.Provider, &f.Account, &f.Group, &f.Model, &f.State, &f.Message, &f.Revision, &result, &base, &key, &f.UpdatedAtMS); err != nil {
			break
		}
		f.Result = json.RawMessage(result)
		// A rotated site credential must not inherit an old identity result.
		if f.Account != "" && f.State == "done" && f.Revision != tokenHash(base+"\n"+key) {
			continue
		}
		data = append(data, f)
	}
	if err == nil {
		err = rows.Err()
	}
	if err != nil {
		return nil, err
	}

	return data, nil
}
