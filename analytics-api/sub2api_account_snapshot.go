package main

import (
	"context"
	"encoding/json"
	"log"
)

type subAccountReadFailure struct {
	message string
	cause   error
}

func (e subAccountReadFailure) Error() string { return e.message }
func (e subAccountReadFailure) Unwrap() error { return e.cause }

func readSubAccountHeaders(ctx context.Context, db controlQueryDB, owner, accountID string) ([]subAccount, error) {
	rows, err := db.QueryContext(ctx, `SELECT id,name,base,email,state,message,synced_at,balance,job_kind,provider_kind,login_name,encrypted_login_password<>'',COALESCE(sync_models,'null'::jsonb) FROM console_sub_accounts WHERE owner=$1 AND ($2='' OR id=$2) ORDER BY created_at,id`, owner, accountID)
	if err != nil {
		return nil, subAccountReadFailure{"账号列表暂不可用", err}
	}
	accounts := []subAccount{}
	for rows.Next() {
		var a subAccount
		var balanceRaw, syncRaw []byte
		if err = rows.Scan(&a.ID, &a.Name, &a.Base, &a.Email, &a.State, &a.Message, &a.SyncedAt, &balanceRaw, &a.JobKind, &a.ProviderKind, &a.LoginName, &a.HasSavedPassword, &syncRaw); err != nil {
			break
		}
		if err = json.Unmarshal(syncRaw, &a.SyncModels); err != nil {
			break
		}
		a.SyncOnly = a.SyncModels != nil && len(a.SyncModels) == 0 && (a.JobKind == "sync" || a.JobKind == "")
		if len(balanceRaw) > 0 {
			if err = json.Unmarshal(balanceRaw, &a.Balance); err != nil {
				break
			}
		}
		a.Targets = []subTarget{}
		accounts = append(accounts, a)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, subAccountReadFailure{"账号列表读取失败", err}
	}

	return accounts, nil
}

func (s *Service) readSubAccountDetailsFrom(ctx context.Context, db controlQueryDB, owner, accountID string, groupID int64, model string, summary, omitRates bool, accounts []subAccount, trace *subAccountsReadTrace) error {
	shared, err := s.sharedQualityForAccountFrom(ctx, db, owner, accountID)
	if err != nil {
		return subAccountReadFailure{"共享检测记录暂不可用", err}
	}
	return s.readSubAccountDetailsWithQualityFrom(ctx, db, owner, accountID, groupID, model, summary, omitRates, accounts, trace, shared)
}

func (s *Service) readSubAccountDetailsWithQualityFrom(ctx context.Context, db controlQueryDB, owner, accountID string, groupID int64, model string, summary, omitRates bool, accounts []subAccount, trace *subAccountsReadTrace, shared sharedQuality) error {
	return s.readSubAccountDetailsWithModelsFrom(ctx, db, owner, accountID, groupID, model, summary, omitRates, accounts, trace, shared, nil, nil, false)
}

func (s *Service) readSubAccountDetailsWithModelsFrom(ctx context.Context, db controlQueryDB, owner, accountID string, groupID int64, model string, summary, omitRates bool, accounts []subAccount, trace *subAccountsReadTrace, shared sharedQuality, readModels func([]subAccount) error, readRates func() (map[string]map[string]subObservedRate, error), compactCapabilities bool) error {
	return s.readSubAccountDetailsWithReadersFrom(ctx, db, owner, accountID, groupID, model, summary, omitRates, accounts, trace, shared, readModels, readRates, compactCapabilities, nil)
}

// Only management's RR owner can provide a summary receipt reader. Ordinary
// account/detail/action callers keep the complete raw receipt path above.
func (s *Service) readSubAccountDetailsWithReadersFrom(ctx context.Context, db controlQueryDB, owner, accountID string, groupID int64, model string, summary, omitRates bool, accounts []subAccount, trace *subAccountsReadTrace, shared sharedQuality, readModels func([]subAccount) error, readRates func() (map[string]map[string]subObservedRate, error), compactCapabilities bool, readUsage func([]subAccount) error) error {
	index := map[string]int{}
	for i, a := range accounts {
		index[a.ID] = i
	}
	trace.next("targets")
	stages := newManagementReadTimeline(ctx, "database_targets")
	defer stages.finish()
	capabilityColumns := "t.compaction,t.compaction_state,t.tool_use,t.tool_use_state"
	capabilityJoin := ""
	historyColumns := "COALESCE(h.total,0),COALESCE(h.successful,0),COALESCE(h.passed,0)"
	historyJoin := " LEFT JOIN console_quality_totals h ON h.account_id=t.account_id AND h.group_id=t.group_id"
	if compactCapabilities && shared.complete && shared.owner == owner && (shared.account == "" || shared.account == accountID) {
		// The complete group projection includes every directly owned history
		// row as well as immutable linked history. A missing group has zero
		// attempts; present groups are attached below. Do not aggregate the same
		// history a second time only to overwrite it with this snapshot.
		historyColumns, historyJoin = "0,0,0", ""
	}
	if summary && compactCapabilities {
		// Only management's compact representation omits these attempt bodies.
		// Detail readers retain the original full diagnostics. The SQL function
		// falls back to raw JSON for any unsupported or malformed representation.
		capabilityJoin = " LEFT JOIN console_sub_capability_summaries cs ON cs.account_id=t.account_id AND cs.group_id=t.group_id"
		capabilityColumns = "CASE WHEN cs.account_id IS NULL THEN console_sub_capability_summary_v1(t.compaction) ELSE cs.compaction END,t.compaction_state,CASE WHEN cs.account_id IS NULL THEN console_sub_capability_summary_v1(t.tool_use) ELSE cs.tool_use END,t.tool_use_state"
	}
	consume := func(row managementTargetRecord, readTrace *managementRowsTrace) error {
		t := row.target
		decodeStart := readTrace.decodeStart()
		if len(row.tools) > 0 {
			if err := json.Unmarshal(row.tools, &t.ToolUse); err != nil {
				return err
			}
		}
		if len(row.compact) > 0 {
			if err := json.Unmarshal(row.compact, &t.Compaction); err != nil {
				return err
			}
		}
		t.Models = []subModelResult{}
		if len(row.billing) > 0 {
			if err := json.Unmarshal(row.billing, &t.Billing); err != nil {
				return err
			}
		}
		if len(row.result) > 0 {
			if err := json.Unmarshal(row.result, &t.Result); err != nil {
				return err
			}
		}
		readTrace.decoded(decodeStart, len(row.result)+len(row.billing)+len(row.compact)+len(row.tools))
		if i, ok := index[row.account]; ok {
			if projection, exists := shared.Groups[qualityGroupID(row.account, t.GroupID)]; exists {
				t.History = projection.History
				t.QualityCheck = projection.Check
			}
			accounts[i].Targets = append(accounts[i].Targets, t)
			if trace != nil {
				trace.targets++
			}
		}
		return nil
	}
	read := func(consume managementTargetConsumer) error {
		readTrace := newManagementRowsTrace(ctx, "target_rows")
		defer readTrace.finish()
		rows, err := db.QueryContext(ctx, `SELECT t.account_id,t.group_id,t.name,t.platform,t.channel,t.rate,t.remote_key_id,t.active,t.state,t.message,t.result,t.billing,`+historyColumns+`,`+capabilityColumns+` FROM console_sub_targets t JOIN console_sub_accounts a ON a.id=t.account_id`+historyJoin+capabilityJoin+` WHERE a.owner=$1 AND ($2='' OR a.id=$2) AND ($3=0 OR t.group_id=$3) ORDER BY t.group_id`, owner, accountID, groupID)
		if err != nil {
			return subAccountReadFailure{"检测结果暂不可用", err}
		}
		readTrace.queryReady()
		defer rows.Close()
		for readTrace.next(rows) {
			var row managementTargetRecord
			t := &row.target
			if err = rows.Scan(&row.account, &t.GroupID, &t.Name, &t.Platform, &t.Channel, &t.Rate, &t.KeyID, &t.Active, &t.State, &t.Message, &row.result, &row.billing, &t.History.Total, &t.History.Successful, &t.History.Passed, &row.compact, &t.CompactionState, &row.tools, &t.ToolUseState); err != nil {
				break
			}
			if err = consume(row, readTrace); err != nil {
				break
			}
		}
		if err == nil {
			err = rows.Err()
		}
		if err != nil {
			return subAccountReadFailure{"检测结果读取失败", err}
		}
		return nil
	}
	// The zero-history query is independent of live quality evidence, which
	// consume always overlays from this request's freshly read projection.
	cacheTargets := summary && compactCapabilities && historyJoin == "" && accountID == "" && groupID == 0
	err := readManagementTargetStream(ctx, db, owner, cacheTargets, read, consume)
	if err != nil {
		return err
	}
	stages.next("database_models")
	trace.next("models")
	if readModels != nil {
		err = readModels(accounts)
	} else {
		var models, modelBytes int
		models, modelBytes, err = s.subReadModelsFrom(ctx, db, owner, accountID, groupID, model, summary, accounts)
		if trace != nil {
			trace.models, trace.modelBytes = models, modelBytes
		}
	}
	if err != nil {
		return subAccountReadFailure{"检测结果读取失败", err}
	}
	stages.next("database_queued")
	trace.next("queued_checks")
	if err := s.subAttachQueuedChecksFrom(ctx, db, owner, accounts); err != nil {
		return subAccountReadFailure{"排队任务读取失败", err}
	}
	stages.next("database_usage")
	trace.next("usage")
	if readUsage == nil {
		readUsage = func(accounts []subAccount) error { return s.subAttachUsageFrom(ctx, db, owner, accounts) }
	}
	if err := readUsage(accounts); err != nil {
		log.Printf("sub_accounts_receipt_read_failed class=%s canceled=%t request_id=%s", databaseErrorClass(err), ctx.Err() != nil, consoleRequestID(ctx))
		return subAccountReadFailure{"模型账单读取失败，请重新读取", err}
	}
	stages.next("database_rates")
	trace.next("observed_rates")
	if !omitRates {
		if readRates == nil {
			readRates = func() (map[string]map[string]subObservedRate, error) {
				return s.control.subObservedRatesForAccountFrom(ctx, db, owner, accountID)
			}
		}
		observed, err := readRates()
		if err != nil {
			return subAccountReadFailure{"账单倍率读取失败", err}
		}
		for accountIndex := range accounts {
			for targetIndex := range accounts[accountIndex].Targets {
				target := &accounts[accountIndex].Targets[targetIndex]
				target.ObservedRates = observed[qualityGroupID(accounts[accountIndex].ID, target.GroupID)]
			}
		}
	}
	return nil
}
