package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

func managementPredicateResult(result *subResult) *subResult {
	if result == nil {
		return nil
	}
	probe := result.Availability
	out := &subResult{CheckedAt: result.CheckedAt, Verdict: result.Verdict,
		Availability: subProbe{Status: probe.Status, TerminalStatus: probe.TerminalStatus, ModelMatch: probe.ModelMatch}}
	if usage := probe.Usage; usage != nil {
		out.Availability.Usage = &subUsage{Status: usage.Status, InputPrice: usage.InputPrice, OutputPrice: usage.OutputPrice,
			InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens}
	}
	return out
}

// All predicate values come from one PostgreSQL statement snapshot. Keep every
// model identity (including defaults, aliases and queued checks added later),
// but do not transfer probe replies/timings or historical receipt payloads.
// The receipt join uses the same owner-wide authorization as subAttachUsage;
// a receipt owned by a different account of this owner is still valid evidence.
const managementModelSignalsSQL = `WITH model_rows AS (
 SELECT m.account_id,m.group_id,m.model,m.state,
 COALESCE(ms.result,console_sub_model_summary_v1(m.result)) AS result
 FROM console_sub_models m
 JOIN console_sub_targets t USING(account_id,group_id)
 JOIN console_sub_accounts a ON a.id=m.account_id
 LEFT JOIN console_sub_model_summaries ms USING(account_id,group_id,model)
 WHERE a.owner=$1
), evidence AS (
 SELECT m.*,u.id AS receipt_id,u.status AS receipt_status,
 CASE WHEN u.id IS NOT NULL THEN u.result ELSE m.result#>'{availability,usage}' END AS usage
 FROM model_rows m LEFT JOIN console_sub_usage u
 ON u.id=NULLIF(m.result#>>'{availability,id}','')
 AND EXISTS(SELECT 1 FROM console_sub_accounts a WHERE a.id=u.account_id AND a.owner=$1)
)
SELECT account_id,group_id,model,state,jsonb_build_array(
 (result IS NULL OR jsonb_typeof(result) IN ('object','null'))
 AND (result->'availability' IS NULL OR jsonb_typeof(result->'availability') IN ('object','null'))
 AND (usage IS NULL OR jsonb_typeof(usage) IN ('object','null')),
 result IS NOT NULL AND result<>'null'::jsonb,
 result->'checked_at',result->'verdict',result#>'{availability,status}',
 result#>'{availability,terminal_status}',result#>'{availability,model_match}',
 receipt_id IS NOT NULL OR (usage IS NOT NULL AND usage<>'null'::jsonb),
 CASE WHEN receipt_id IS NOT NULL THEN COALESCE(NULLIF(usage->'status','null'::jsonb),to_jsonb(receipt_status)) ELSE usage->'status' END,
 usage->'input_price',usage->'output_price',usage->'input_tokens',usage->'output_tokens')
FROM evidence ORDER BY account_id,group_id,model`

// A typed relational form avoids building/decoding a JSON tuple per model.
// Fixed expressions are not user input. Type guards preserve rejection of
// invalid JSON values instead of coercing e.g. a quoted price into a number.
var managementModelSignalsTypedSQL = func() string {
	cte, _, _ := strings.Cut(managementModelSignalsSQL, "SELECT account_id,group_id,model,state,jsonb_build_array(")
	valid := []string{
		"(result IS NULL OR jsonb_typeof(result) IN ('object','null'))",
		"(result->'availability' IS NULL OR jsonb_typeof(result->'availability') IN ('object','null'))",
		"(usage IS NULL OR jsonb_typeof(usage) IN ('object','null'))",
	}
	values := []string{"result IS NOT NULL AND result<>'null'::jsonb"}
	for _, field := range []struct{ expr, kind, cast, fallback string }{
		{"result->'checked_at'", "number", "bigint", "0"},
		{"result->'verdict'", "string", "text", "''"},
		{"result#>'{availability,status}'", "string", "text", "''"},
		{"result#>'{availability,terminal_status}'", "string", "text", "''"},
		{"result#>'{availability,model_match}'", "string", "text", "''"},
		{"CASE WHEN receipt_id IS NOT NULL THEN COALESCE(NULLIF(usage->'status','null'::jsonb),to_jsonb(receipt_status)) ELSE usage->'status' END", "string", "text", "''"},
		{"usage->'input_price'", "number", "double precision", "NULL"},
		{"usage->'output_price'", "number", "double precision", "NULL"},
		{"usage->'input_tokens'", "number", "bigint", "NULL"},
		{"usage->'output_tokens'", "number", "bigint", "NULL"},
	} {
		expr := "(" + field.expr + ")"
		valid = append(valid, "("+expr+" IS NULL OR jsonb_typeof("+expr+") IN ('null','"+field.kind+"'))")
		values = append(values, "COALESCE(CASE WHEN jsonb_typeof("+expr+")='"+field.kind+"' THEN ("+expr+"#>>'{}')::"+field.cast+" END,"+field.fallback+")")
	}
	values = append(values, "receipt_id IS NOT NULL OR (usage IS NOT NULL AND usage<>'null'::jsonb)")
	return cte + "SELECT account_id,group_id,model,state," + strings.Join(valid, " AND ") + "," + strings.Join(values, ",") + " FROM evidence ORDER BY account_id,group_id,model"
}()

func readManagementModelSignalsTypedFrom(ctx context.Context, db controlQueryDB, owner string) ([]managementModelSignals, error) {
	return readManagementSignalsColumns(ctx, db, owner, managementModelSignalsTypedSQL)
}

func decodeManagementModelSignals(raw []byte) (*subResult, error) {
	var fields []json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	if len(fields) != 13 {
		return nil, errors.New("incomplete management predicate projection")
	}
	var valid, present, hasUsage bool
	var result subResult
	var usage subUsage
	targets := []any{&valid, &present, &result.CheckedAt, &result.Verdict, &result.Availability.Status,
		&result.Availability.TerminalStatus, &result.Availability.ModelMatch, &hasUsage,
		&usage.Status, &usage.InputPrice, &usage.OutputPrice, &usage.InputTokens, &usage.OutputTokens}
	for i, target := range targets {
		if err := json.Unmarshal(fields[i], target); err != nil {
			return nil, err
		}
	}
	if !valid {
		return nil, errors.New("invalid management predicate evidence")
	}
	if !present {
		return nil, nil
	}
	if hasUsage {
		result.Availability.Usage = &usage
	}
	return &result, nil
}

func readManagementModelSignalsFrom(ctx context.Context, db controlQueryDB, owner string) ([]managementModelSignals, int, error) {
	rows, err := db.QueryContext(ctx, managementModelSignalsSQL, owner)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []managementModelSignals{}
	bytes := 0
	for rows.Next() {
		var item managementModelSignals
		var raw []byte
		if err := rows.Scan(&item.Account, &item.Group, &item.Model.Model, &item.Model.State, &raw); err != nil {
			return nil, bytes, err
		}
		bytes += len(raw)
		item.Model.Result, err = decodeManagementModelSignals(raw)
		if err != nil {
			return nil, bytes, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, bytes, err
	}
	if err := ctx.Err(); err != nil {
		return nil, bytes, err
	}
	return out, bytes, nil
}
