package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

// Recovery reads already billed requests on the account's current dedicated
// probe key. It never invokes a model, creates a key, or changes a channel.
// An account/group/key mismatch cannot become evidence for another channel.
func (s *controlStore) retainObservedRatePage(ctx context.Context, account string, group, key int64, records []subUsageLog, apply bool) (int, error) {
	latest := map[string]subUsageLog{}
	for _, record := range records {
		if record.KeyID != key || record.GroupID != nil && *record.GroupID != group || record.Model == "" || record.ID <= 0 {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, record.CreatedAt)
		if err != nil || at.After(time.Now().Add(time.Minute)) {
			continue
		}
		previous, ok := latest[record.Model]
		previousAt, _ := time.Parse(time.RFC3339Nano, previous.CreatedAt)
		if !ok || at.After(previousAt) || at.Equal(previousAt) && record.ID > previous.ID {
			latest[record.Model] = record
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var current int64
	if err = tx.QueryRowContext(ctx, `SELECT remote_key_id FROM console_sub_targets WHERE account_id=$1 AND group_id=$2 FOR SHARE`, account, group).Scan(&current); err != nil {
		return 0, err
	}
	if current != key {
		return 0, errors.New("billing key changed during recovery")
	}
	count := 0
	for model, record := range latest {
		usage := subUsageReceipt(record)
		at, _ := time.Parse(time.RFC3339Nano, record.CreatedAt)
		verified := subReceiptRate(*usage, at.Unix()) != nil
		// Only the fields used by subReceiptRate are durable metadata; no raw
		// response, account token, request payload or ledger history is retained.
		raw, _ := json.Marshal(map[string]any{"status": usage.Status, "actual_cost": usage.ActualCost, "total_cost": usage.TotalCost, "rate_multiplier": usage.Rate, "input_price": usage.InputPrice, "output_price": usage.OutputPrice, "created_at": usage.CreatedAt})
		if !apply {
			if verified {
				count++
			}
			continue
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO console_sub_observed_rates(account_id,group_id,model,key_id,started_at,receipt_id,result)
 VALUES($1,$2,$3,$4,$5,$6,$7)
 ON CONFLICT(account_id,group_id,model) DO UPDATE SET key_id=EXCLUDED.key_id,started_at=EXCLUDED.started_at,receipt_id=EXCLUDED.receipt_id,result=EXCLUDED.result
 WHERE console_sub_observed_rates.key_id<>EXCLUDED.key_id OR console_sub_observed_rates.started_at<EXCLUDED.started_at`, account, group, model, key, at.Unix(), "ledger:"+strconv.FormatInt(record.ID, 10), string(raw))
		if err != nil {
			return 0, err
		}
		n, _ := result.RowsAffected()
		if verified {
			count += int(n)
		}
	}
	if !apply {
		return count, nil
	}
	return count, tx.Commit()
}

func recoverObservedRates(args []string) error {
	flags := flag.NewFlagSet("recover-observed-rates", flag.ContinueOnError)
	account := flags.String("account", "", "inspect only this saved account")
	group := flags.Int64("group", 0, "inspect only this group (requires --account)")
	afterAccount := flags.String("after-account", "", "resume after this account/group cursor")
	afterGroup := flags.Int64("after-group", 0, "resume after this group within the cursor account")
	limit := flags.Int("limit", 4, "maximum groups in this invocation (1–12)")
	apply := flags.Bool("apply", false, "persist only verified receipt-derived model multipliers")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *limit < 1 || *limit > 12 {
		return errors.New("recovery group limit must be between 1 and 12")
	}
	if *group < 0 || *group > 0 && *account == "" {
		return errors.New("a recovery group requires its saved account")
	}
	store, err := newControlStore(env("CONTROL_DATABASE_URL", env("DATABASE_URL", "")), env("CONTROL_MASTER_KEY", ""))
	if err != nil {
		return err
	}
	defer store.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	rows, err := store.db.QueryContext(ctx, `SELECT t.account_id,t.group_id,t.remote_key_id,a.base FROM console_sub_targets t JOIN console_sub_accounts a ON a.id=t.account_id
 WHERE t.remote_key_id>0 AND ($1='' OR t.account_id=$1) AND (t.account_id,t.group_id)>($2,$3) AND ($5::bigint=0 OR t.group_id=$5) ORDER BY t.account_id,t.group_id LIMIT $4`, *account, *afterAccount, *afterGroup, *limit, *group)
	if err != nil {
		return err
	}
	type target struct {
		account, base string
		group, key    int64
	}
	var targets []target
	for rows.Next() {
		var v target
		if err = rows.Scan(&v.account, &v.group, &v.key, &v.base); err != nil {
			rows.Close()
			return err
		}
		targets = append(targets, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	s := &Service{control: store}
	for _, t := range targets {
		readCtx, stop := context.WithTimeout(ctx, 20*time.Second)
		q := url.Values{"api_key_id": {strconv.FormatInt(t.key, 10)}, "page": {"1"}, "page_size": {"100"}, "sort_by": {"created_at"}, "sort_order": {"desc"}}
		var page struct {
			Items []subUsageLog `json:"items"`
			Page  int           `json:"page"`
		}
		err = s.subUsageGET(readCtx, t.account, t.base, "/api/v1/usage?"+q.Encode(), &page)
		n := 0
		if err == nil && (page.Items == nil || len(page.Items) > 100 || page.Page != 0 && page.Page != 1) {
			err = errors.New("invalid receipt recovery page")
		}
		if err == nil {
			n, err = store.retainObservedRatePage(readCtx, t.account, t.group, t.key, page.Items, *apply)
		}
		stop()
		out := map[string]any{"account_id": t.account, "group_id": t.group, "models": n, "apply": *apply, "success": err == nil}
		if err != nil {
			out["cause"] = subSpendErrorCode(err)
		}
		raw, _ := json.Marshal(out)
		fmt.Println(string(raw))
		if !waitStartup(ctx, 250*time.Millisecond) {
			return ctx.Err()
		}
	}
	return nil
}
