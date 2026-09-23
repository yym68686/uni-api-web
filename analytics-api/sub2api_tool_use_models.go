package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Legacy results prove only their named model. A group summary is never copied
// to siblings. Keep each completed result if a later model fails or is stopped.
func toolUseModelResults(saved *subCapabilityResult) map[string]*subCapabilityResult {
	out := map[string]*subCapabilityResult{}
	if saved == nil {
		return out
	}
	for _, m := range saved.Models {
		if m.Result != nil {
			out[m.Model] = m.Result
		}
	}
	if len(saved.Models) == 0 && saved.Model != "" {
		out[saved.Model] = saved
	}
	return out
}

func summarizeToolUse(models []subToolUseModel) subCapabilityResult {
	out := subCapabilityResult{Status: "supported", Models: models, Attempts: []subProbe{}, CheckedAt: time.Now().Unix()}
	passed, failed, unknown := 0, 0, 0
	for _, m := range models {
		if m.Result != nil {
			out.Attempts = append(out.Attempts, m.Result.Attempts...)
		}
		if m.State != "done" || m.Result == nil {
			unknown++
			continue
		}
		switch m.Result.Status {
		case "supported":
			passed++
		case "unsupported":
			failed++
		default:
			unknown++
		}
	}
	if unknown > 0 || len(models) == 0 {
		out.Status = "error"
	}
	if failed > 0 {
		out.Status = "unsupported"
	}
	out.Message = fmt.Sprintf("%d/%d 个可用模型支持工具调用；%d 个不支持，%d 个未完成或检测失败", passed, len(models), failed, unknown)
	if len(models) == 0 {
		out.Message = "没有已检测可用的模型，请先运行模型检测"
	}
	return out
}

func (s *Service) subTestAllModelTools(ctx context.Context, id, base, job string) error {
	rows, err := s.control.db.QueryContext(ctx, `SELECT group_id,encrypted_key,remote_key_id,tool_use,
 COALESCE((SELECT jsonb_agg(jsonb_build_object('model',m.model,'state',m.state,'result',m.result)) FROM console_sub_models m WHERE m.account_id=t.account_id AND m.group_id=t.group_id),'[]'::jsonb)
 FROM console_sub_targets t WHERE account_id=$1 AND active AND tool_use_state='queued'`, id)
	if err != nil {
		return err
	}
	type target struct {
		group, keyID int64
		key          string
		models       []subModelResult
		previous     *subCapabilityResult
	}
	var targets []target
	for rows.Next() {
		var t target
		var raw, previous []byte
		if err = rows.Scan(&t.group, &t.key, &t.keyID, &previous, &raw); err != nil {
			break
		}
		if err = json.Unmarshal(raw, &t.models); err != nil {
			break
		}
		if len(previous) > 0 {
			if err = json.Unmarshal(previous, &t.previous); err != nil {
				break
			}
		}
		targets = append(targets, t)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	run := func(t target) error {
		key, e := s.control.decrypt(t.key)
		if e != nil {
			return errors.New("Tool use 检测 key 无法解密")
		}
		previous := toolUseModelResults(t.previous)
		models := []subToolUseModel{}
		for _, model := range subCompactionModels(t.models) {
			models = append(models, subToolUseModel{Model: model, State: "queued", Result: previous[model]})
		}
		save := func(state string) error {
			result := summarizeToolUse(models)
			res, e := s.control.db.ExecContext(ctx, `UPDATE console_sub_targets SET tool_use_state=$4,tool_use=$5,
 state=CASE WHEN $4='done' AND state IN ('queued','running') THEN 'done' ELSE state END
 WHERE account_id=$1 AND group_id=$2 AND EXISTS(SELECT 1 FROM console_sub_accounts WHERE id=$1 AND job_id=$3 AND state='running')`, id, t.group, job, state, mustJSON(result))
			if e != nil {
				return e
			}
			n, _ := res.RowsAffected()
			if n != 1 {
				return context.Canceled
			}
			return nil
		}
		if e = save("running"); e != nil {
			return e
		}
		for i := range models {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			models[i].State = "running"
			if e = save("running"); e != nil {
				return e
			}
			probeCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
			probe := subProbeToolUse(probeCtx, subHTTP, base, key, models[i].Model)
			cancel()
			usage := subResult{Model: models[i].Model, Availability: probe}
			if s.subQueueUsage(ctx, id, t.group, t.keyID, &usage) == nil {
				probe = usage.Availability
			}
			models[i].Result = &subCapabilityResult{Model: models[i].Model, Status: probe.Status, Message: probe.Message, CheckedAt: time.Now().Unix(), Attempts: []subProbe{probe}}
			models[i].State = "done"
			if e = save("running"); e != nil {
				return e
			}
		}
		return save("done")
	}
	// Same concurrency bound as the existing group worker; models are serial.
	jobs := make(chan target)
	errs := make(chan error, len(targets))
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for t := range jobs {
				if e := run(t); e != nil {
					errs <- e
				}
			}
		}()
	}
send:
	for _, t := range targets {
		select {
		case jobs <- t:
		case <-ctx.Done():
			break send
		}
	}
	close(jobs)
	workers.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			return e
		}
	}
	return ctx.Err()
}
