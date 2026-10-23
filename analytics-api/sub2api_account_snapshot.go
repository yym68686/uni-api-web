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
	rows, err := db.QueryContext(ctx, `SELECT id,name,base,email,state,message,synced_at,balance,job_kind,provider_kind,login_name,encrypted_login_password<>'' FROM console_sub_accounts WHERE owner=$1 AND ($2='' OR id=$2) ORDER BY created_at,id`, owner, accountID)
	if err != nil {
		return nil, subAccountReadFailure{"账号列表暂不可用", err}
	}
	accounts := []subAccount{}
	for rows.Next() {
		var a subAccount
		var balanceRaw []byte
		if err = rows.Scan(&a.ID, &a.Name, &a.Base, &a.Email, &a.State, &a.Message, &a.SyncedAt, &balanceRaw, &a.JobKind, &a.ProviderKind, &a.LoginName, &a.HasSavedPassword); err != nil {
			break
		}
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
	return s.readSubAccountDetailsWithModelsFrom(ctx, db, owner, accountID, groupID, model, summary, omitRates, accounts, trace, shared, nil)
}

func (s *Service) readSubAccountDetailsWithModelsFrom(ctx context.Context, db controlQueryDB, owner, accountID string, groupID int64, model string, summary, omitRates bool, accounts []subAccount, trace *subAccountsReadTrace, shared sharedQuality, readModels func([]subAccount) error) error {
	index := map[string]int{}
	for i, a := range accounts {
		index[a.ID] = i
	}
	trace.next("targets")
	stages := newManagementReadTimeline(ctx, "database_targets")
	defer stages.finish()
	rows, err := db.QueryContext(ctx, `SELECT t.account_id,t.group_id,t.name,t.platform,t.channel,t.rate,t.remote_key_id,t.active,t.state,t.message,t.result,t.billing,COALESCE(h.total,0),COALESCE(h.successful,0),COALESCE(h.passed,0),t.compaction,t.compaction_state,t.tool_use,t.tool_use_state FROM console_sub_targets t JOIN console_sub_accounts a ON a.id=t.account_id LEFT JOIN console_quality_totals h ON h.account_id=t.account_id AND h.group_id=t.group_id WHERE a.owner=$1 AND ($2='' OR a.id=$2) AND ($3=0 OR t.group_id=$3) ORDER BY t.group_id`, owner, accountID, groupID)
	if err != nil {
		return subAccountReadFailure{"检测结果暂不可用", err}
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var t subTarget
		var raw, billingRaw, compactionRaw, toolUseRaw []byte
		if err = rows.Scan(&id, &t.GroupID, &t.Name, &t.Platform, &t.Channel, &t.Rate, &t.KeyID, &t.Active, &t.State, &t.Message, &raw, &billingRaw, &t.History.Total, &t.History.Successful, &t.History.Passed, &compactionRaw, &t.CompactionState, &toolUseRaw, &t.ToolUseState); err != nil {
			break
		}
		if len(toolUseRaw) > 0 {
			if err = json.Unmarshal(toolUseRaw, &t.ToolUse); err != nil {
				break
			}
		}
		if len(compactionRaw) > 0 {
			if err = json.Unmarshal(compactionRaw, &t.Compaction); err != nil {
				break
			}
		}
		t.Models = []subModelResult{}
		if len(billingRaw) > 0 {
			if err = json.Unmarshal(billingRaw, &t.Billing); err != nil {
				break
			}
		}
		if len(raw) > 0 {
			if err = json.Unmarshal(raw, &t.Result); err != nil {
				break
			}
		}
		if i, ok := index[id]; ok {
			if projection, exists := shared.Groups[qualityGroupID(id, t.GroupID)]; exists {
				t.History = projection.History
				t.QualityCheck = projection.Check
			}
			accounts[i].Targets = append(accounts[i].Targets, t)
			if trace != nil {
				trace.targets++
			}
		}
	}
	if err == nil {
		err = rows.Err()
	}
	if err != nil {
		return subAccountReadFailure{"检测结果读取失败", err}
	}
	rows.Close()
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
	if err := s.subAttachUsageFrom(ctx, db, owner, accounts); err != nil {
		log.Printf("sub_accounts_receipt_read_failed class=%s canceled=%t request_id=%s", databaseErrorClass(err), ctx.Err() != nil, consoleRequestID(ctx))
		return subAccountReadFailure{"模型账单读取失败，请重新读取", err}
	}
	stages.next("database_rates")
	trace.next("observed_rates")
	if !omitRates {
		observed, err := s.control.subObservedRatesForAccountFrom(ctx, db, owner, accountID)
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
