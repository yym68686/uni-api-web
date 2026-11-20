package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"math/big"
	"sort"
	"strconv"
	"time"
)

const spendSliceHour int64 = 3600
const spendSliceDay = 24 * spendSliceHour
const spendSliceQuarter = spendSliceHour / 4

type spendCapture struct {
	persist, unsafe                        bool
	binding                                string
	identities, receipts, claims, evidence []string
	accounts                               map[string]map[string][2]int64
	amounts                                map[string]map[string]*big.Rat
	sampleIDs                              map[string][]string
}

func newSpendCapture(persist bool) *spendCapture {
	return &spendCapture{persist: persist, accounts: map[string]map[string][2]int64{}, amounts: map[string]map[string]*big.Rat{}, sampleIDs: map[string][]string{}}
}

func (c *spendCapture) account(b billingFact, account string) {
	id := billingRowID(b.Source, b.Provider, b.Model, b.Upstream)
	if c.accounts[id] == nil {
		c.accounts[id] = map[string][2]int64{}
	}
	from := max(0, min(b.Started, b.At)-60000)
	end := b.At + 60000
	if old, ok := c.accounts[id][account]; ok {
		from, end = min(from, old[0]), max(end, old[1])
	}
	c.accounts[id][account] = [2]int64{from, end}
}

func (c *spendCapture) addCost(b billingFact, account string, value *big.Rat) {
	id := billingRowID(b.Source, b.Provider, b.Model, b.Upstream)
	if c.amounts[id] == nil {
		c.amounts[id] = map[string]*big.Rat{}
	}
	if c.amounts[id][account] == nil {
		c.amounts[id][account] = new(big.Rat)
	}
	c.amounts[id][account].Add(c.amounts[id][account], value)
}

func (c *spendCapture) markSample(row *attributedSpend, b billingFact, reason string) {
	before := len(row.UnresolvedSamples)
	row.unresolved(b, reason)
	if c != nil && len(row.UnresolvedSamples) != before {
		id := billingRowID(b.Source, b.Provider, b.Model, b.Upstream)
		c.sampleIDs[id] = append(c.sampleIDs[id], b.Event)
	}
}

// Settings are re-read on every fast path. Rate changes are deliberately not
// part of the identity: every slice stores raw exact subtotals per account.
func spendBindingDigest(bindings map[string][]receiptScope) string {
	rows := make([]string, 0, len(bindings))
	for key, scopes := range bindings {
		for _, v := range scopes {
			rows = append(rows, mustJSON([]any{key, v.Account, v.Key, v.Kind}))
		}
	}
	sort.Strings(rows)
	return tokenHash(mustJSON(rows))
}

func spendSliceBindings(bindings map[string][]receiptScope) (map[string]float64, bool) {
	aliases := map[string]string{}
	rates := map[string]float64{}
	for key, scopes := range bindings {
		for _, v := range scopes {
			id := receiptIdentity(v.Account, v.Key, "")
			if other, ok := aliases[id]; ok && other != key {
				// The old matcher deduplicates this receipt number across those
				// aliases in event order. Such windows cannot be safely split.
				return nil, false
			}
			aliases[id] = key
			rates[v.Account] = siteRateCoefficient(v.RateCoefficient)
		}
	}
	return rates, true
}

type spendSliceWindow struct{ from, to int64 }
type spendSliceJob struct {
	spendSliceWindow
	owner, scope, binding string
	filter                QueryFilter
	verify                bool
}

// This alias bypasses the external JSON compatibility logic; the wire amount
// is finalized only after all slices have been merged for the actual window.
type storedSpendRow attributedSpend
type spendSliceRow struct {
	Row       storedSpendRow
	Amounts   map[string]string
	Accounts  map[string][2]int64
	SampleIDs []string
}

func slicePayload(rows []attributedSpend, c *spendCapture) []spendSliceRow {
	out := make([]spendSliceRow, 0, len(rows))
	for _, row := range rows {
		id := billingRowID(row.Source, row.Provider, row.Model, row.UpstreamModel)
		amounts := make(map[string]string, len(c.amounts[id]))
		for k, v := range c.amounts[id] {
			amounts[k] = v.RatString()
		}
		out = append(out, spendSliceRow{storedSpendRow(row), amounts, c.accounts[id], c.sampleIDs[id]})
	}
	return out
}

func canonicalSpendSlices(from, to int64) []spendSliceWindow {
	var slices []spendSliceWindow
	if to-from < 2*spendSliceHour {
		for at := (from + spendSliceQuarter - 1) / spendSliceQuarter * spendSliceQuarter; at+spendSliceQuarter <= to; at += spendSliceQuarter {
			slices = append(slices, spendSliceWindow{at, at + spendSliceQuarter})
		}
		return slices
	}
	start := (from + spendSliceHour - 1) / spendSliceHour * spendSliceHour
	// No request may schedule arbitrarily many warm jobs. Recent data is most
	// likely to be reused by the next rolling-window refresh.
	start = max(start, to/spendSliceDay*spendSliceDay-120*spendSliceDay)
	for start+spendSliceHour <= to {
		width := spendSliceHour
		if start%spendSliceDay == 0 && start+spendSliceDay <= to {
			width = spendSliceDay
		}
		slices = append(slices, spendSliceWindow{start, start + width})
		start += width
	}
	return slices
}

type spendSliceState struct {
	epoch, floor, oldest int64
	claimsComplete       bool
}

func (s *Service) spendSliceSnapshot(ctx context.Context, scope, binding string, from, to int64) (spendSliceState, map[spendSliceWindow][]spendSliceRow, error) {
	var state spendSliceState
	byWindow := map[spendSliceWindow][]spendSliceRow{}
	tx, err := s.engine.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return state, nil, err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, `SELECT e.epoch,s.evicted_before_ms,s.claims_history_complete,coalesce((SELECT min(start_ms) FROM fact_partitions),0) FROM channel_spend_epoch e,storage_state s WHERE e.singleton AND s.singleton`).Scan(&state.epoch, &state.floor, &state.claimsComplete, &state.oldest)
	if err != nil {
		return state, nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT from_sec,to_sec,payload FROM channel_spend_slices
 WHERE scope=? AND binding=? AND from_sec>=? AND to_sec<=?`, scope, binding, from, to)
	if err != nil {
		return state, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var w spendSliceWindow
		var body []byte
		var payload []spendSliceRow
		if err = rows.Scan(&w.from, &w.to, &body); err != nil {
			return state, nil, err
		}
		if err = json.Unmarshal(body, &payload); err != nil {
			return state, nil, err
		}
		byWindow[w] = payload
	}
	return state, byWindow, rows.Err()
}

func (s *Service) attributedChannelSpend(ctx context.Context, owner string, filter QueryFilter, from, to int64) ([]attributedSpend, error) {
	if s.spendSliceJobs != nil && to-from >= 2*spendSliceQuarter {
		if data, ok := s.composedChannelSpend(ctx, owner, filter, from, to, false); ok {
			return data, nil
		}
	}
	return s.readAttributedChannelSpend(ctx, owner, filter, from, to, nil)
}

func spendSliceScope(owner string, filter QueryFilter) string {
	filter.To, filter.Range, filter.Timeseries = 0, "", false
	filter.SourceIDs = append([]string(nil), filter.SourceIDs...)
	sort.Strings(filter.SourceIDs)
	return tokenHash(mustJSON([]any{owner, filter}))
}

func (s *Service) composedChannelSpend(ctx context.Context, owner string, filter QueryFilter, from, to int64, verifying bool) ([]attributedSpend, bool) {
	bindings, err := s.receiptBindings(ctx, owner)
	if err != nil {
		return nil, false
	}
	_, safe := spendSliceBindings(bindings)
	if !safe {
		return nil, false
	}
	binding := spendBindingDigest(bindings)
	scope := spendSliceScope(owner, filter)
	state, stored, err := s.spendSliceSnapshot(ctx, scope, binding, from, to)
	if err != nil {
		return nil, false
	}
	effectiveFrom := max(from, state.oldest/1000)
	plan := canonicalSpendSlices(max(effectiveFrom, (state.floor+999)/1000), to)
	var parts []spendSliceRow
	var missing []spendSliceWindow
	start, hits := effectiveFrom, 0
	for _, w := range plan {
		if _, ok := stored[w]; !ok && !verifying {
			// Prefer large historical chunks first. Only one capped job is
			// queued per refresh; hot queries never wait for it to finish.
			if s.enqueueSpendSlice(spendSliceJob{spendSliceWindow: w, owner: owner, scope: scope, binding: binding, filter: filter}) {
				break
			}
		}
	}
	for _, w := range plan {
		rows, ok := stored[w]
		if !ok {
			continue
		}
		if start < w.from {
			missing = append(missing, spendSliceWindow{start, w.from})
		}
		start = w.to
		hits++
		parts = append(parts, rows...)
	}
	if start < to {
		missing = append(missing, spendSliceWindow{start, to})
	}
	if hits == 0 || len(missing) > 3 {
		return nil, false
	}
	if !verifying {
		s.spendSliceMu.Lock()
		verified := s.spendSliceVerified[scope+binding]
		s.spendSliceMu.Unlock()
		if !verified {
			s.enqueueSpendSlice(spendSliceJob{spendSliceWindow: spendSliceWindow{from, to}, owner: owner, scope: scope, binding: binding, filter: filter, verify: true})
		}
		if !verified || s.spendSliceShadow {
			return nil, false
		}
	}
	for _, gap := range missing {
		capture := newSpendCapture(false)
		rows, e := s.readAttributedChannelSpend(ctx, owner, filter, gap.from, gap.to, capture)
		if e != nil || capture.unsafe || capture.binding != binding {
			return nil, false
		}
		parts = append(parts, slicePayload(rows, capture)...)
	}
	// Changes concurrent with a split read require a fresh whole-window
	// snapshot; a receipt must never appear in both the old and new epochs.
	latest, err := s.receiptBindings(ctx, owner)
	if err != nil || spendBindingDigest(latest) != binding {
		return nil, false
	}
	rates, safe := spendSliceBindings(latest)
	if !safe {
		return nil, false
	}
	data, ok := s.mergeSpendSlices(ctx, parts, rates, state, from, to, !verifying)
	if !ok {
		return nil, false
	}
	var epoch int64
	if err = s.engine.DB.QueryRowContext(ctx, `SELECT epoch FROM channel_spend_epoch WHERE singleton`).Scan(&epoch); err != nil || epoch != state.epoch {
		return nil, false
	}
	return data, true
}

func (s *Service) mergeSpendSlices(ctx context.Context, parts []spendSliceRow, rates map[string]float64, state spendSliceState, from, to int64, queue bool) ([]attributedSpend, bool) {
	type sample struct {
		id    string
		value unresolvedSpendAttempt
	}
	totals := map[string]*attributedSpend{}
	amounts := map[string]*big.Rat{}
	samples := map[string][]sample{}
	accounts := map[string]map[string]bool{}
	windows := map[string][2]int64{}
	for _, part := range parts {
		r := part.Row
		id := billingRowID(r.Source, r.Provider, r.Model, r.UpstreamModel)
		if totals[id] == nil {
			totals[id] = &attributedSpend{Source: r.Source, Provider: r.Provider, Model: r.Model, UpstreamModel: r.UpstreamModel, From: from, To: to, Scope: "matched_requests"}
			amounts[id] = new(big.Rat)
			accounts[id] = map[string]bool{}
		}
		a := totals[id]
		a.Total += r.Total
		a.Matched += r.Matched
		a.ConfirmedUnbilled += r.ConfirmedUnbilled
		a.Missing += r.Missing
		a.Ambiguous += r.Ambiguous
		a.LegacyMissing += r.LegacyMissing
		a.HeaderMissing += r.HeaderMissing
		a.Unbound += r.Unbound
		a.AbsentReceipts += r.AbsentReceipts
		a.SyncError = a.SyncError || r.SyncError
		addCounts(&a.AbsentStatuses, r.AbsentStatuses)
		addCounts(&a.MissingStatuses, r.MissingStatuses)
		if len(part.SampleIDs) != len(r.UnresolvedSamples) {
			return nil, false
		}
		for i, v := range r.UnresolvedSamples {
			samples[id] = append(samples[id], sample{part.SampleIDs[i], v})
		}
		for account, window := range part.Accounts {
			rate, ok := rates[account]
			if !ok {
				return nil, false
			}
			accounts[id][account] = true
			if rate != 1 {
				if a.RateCoefficients == nil {
					a.RateCoefficients = map[string]float64{}
				}
				a.RateCoefficients[account] = rate
			}
			window[1] = min(time.Now().UnixMilli(), max(to*1000, window[1]))
			if old, ok := windows[account]; ok {
				window[0], window[1] = min(window[0], old[0]), max(window[1], old[1])
			}
			windows[account] = window
		}
		for account, value := range part.Amounts {
			price, ok := new(big.Rat).SetString(value)
			if !ok || !accounts[id][account] {
				return nil, false
			}
			if rates[account] != 1 {
				factor, _ := new(big.Rat).SetString(strconv.FormatFloat(rates[account], 'f', -1, 64))
				price.Mul(price, factor)
			}
			amounts[id].Add(amounts[id], price)
		}
	}
	checked := map[string]int64{}
	if len(windows) > 0 {
		ids := make([]string, 0, len(windows))
		for id := range windows {
			ids = append(ids, id)
		}
		rows, err := s.control.db.QueryContext(ctx, `SELECT account_id,checked_at FROM console_sub_account_spend_cache WHERE account_id=ANY($1)`, ids)
		if err != nil {
			return nil, false
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			var at int64
			if rows.Scan(&id, &at) != nil {
				return nil, false
			}
			checked[id] = at
		}
		if rows.Err() != nil {
			return nil, false
		}
		if queue {
			if err = s.queueAccountSpendWindows(ctx, windows); err != nil {
				return nil, false
			}
		}
	}
	out := make([]attributedSpend, 0, len(totals))
	for id, r := range totals {
		for account := range accounts[id] {
			if at := checked[account]; at > 0 && (r.CheckedAt == 0 || at/1000 < r.CheckedAt) {
				r.CheckedAt = at / 1000
			}
		}
		v := samples[id]
		sort.SliceStable(v, func(i, j int) bool { return v[i].id < v[j].id })
		for i := 0; i < min(len(v), 8); i++ {
			r.UnresolvedSamples = append(r.UnresolvedSamples, v[i].value)
		}
		amount, _ := amounts[id].Float64()
		r.MatchedAmount = amount
		finishAttributedSpend(r, amount, state.claimsComplete, state.floor)
		out = append(out, *r)
	}
	return out, true
}

func addCounts(target *map[string]int, source map[string]int) {
	if len(source) > 0 && *target == nil {
		*target = map[string]int{}
	}
	for key, n := range source {
		(*target)[key] += n
	}
}

func (s *Service) enqueueSpendSlice(job spendSliceJob) bool {
	key := job.scope + ":" + strconv.FormatInt(job.from, 10) + ":" + strconv.FormatInt(job.to, 10)
	if job.verify {
		key = job.scope + job.binding + ":verify"
	}
	s.spendSliceMu.Lock()
	defer s.spendSliceMu.Unlock()
	now := time.Now()
	for k, until := range s.spendSliceNext {
		if now.After(until) {
			delete(s.spendSliceNext, k)
		}
	}
	if now.Before(s.spendSliceNext[key]) || len(s.spendSliceNext) >= 128 {
		return false
	}
	select {
	case s.spendSliceJobs <- job:
		s.spendSliceNext[key] = now.Add(time.Minute)
		return true
	default:
		return false
	}
}

func (s *Service) spendSliceLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-s.spendSliceJobs:
			limit := 3 * time.Second
			if job.verify {
				limit = 10 * time.Second
			}
			call, stop := context.WithTimeout(ctx, limit)
			var err error
			if job.verify {
				err = s.verifySpendSlices(call, job)
			} else {
				err = s.warmSpendSlice(call, job)
			}
			stop()
			if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, errAnalyticsPriority) && ctx.Err() == nil {
				log.Printf("event=channel_spend_slice_deferred class=%s", databaseErrorClass(err))
			}
			if !waitStartup(ctx, time.Second) {
				return
			}
		}
	}
}

func (s *Service) verifySpendSlices(ctx context.Context, job spendSliceJob) error {
	var before, after int64
	if err := s.engine.DB.QueryRowContext(ctx, `SELECT epoch FROM channel_spend_epoch WHERE singleton`).Scan(&before); err != nil {
		return err
	}
	composed, ok := s.composedChannelSpend(ctx, job.owner, job.filter, job.from, job.to, true)
	if !ok {
		return nil
	}
	legacy, err := s.readAttributedChannelSpend(ctx, job.owner, job.filter, job.from, job.to, nil)
	if err != nil {
		return err
	}
	if err = s.engine.DB.QueryRowContext(ctx, `SELECT epoch FROM channel_spend_epoch WHERE singleton`).Scan(&after); err != nil || after != before {
		return err
	}
	bindings, err := s.receiptBindings(ctx, job.owner)
	if err != nil || spendBindingDigest(bindings) != job.binding {
		return err
	}
	if comparableSpend(composed) != comparableSpend(legacy) {
		log.Printf("event=channel_spend_slice_mismatch seconds=%d", job.to-job.from)
		return nil
	}
	s.spendSliceMu.Lock()
	if len(s.spendSliceVerified) >= 128 {
		s.spendSliceVerified = map[string]bool{}
	}
	s.spendSliceVerified[job.scope+job.binding] = true
	s.spendSliceMu.Unlock()
	log.Printf("event=channel_spend_slice_verified seconds=%d rows=%d", job.to-job.from, len(legacy))
	return nil
}

func comparableSpend(rows []attributedSpend) string {
	out := make(map[string]attributedSpend, len(rows))
	for _, r := range rows {
		// A newer synchronization checkpoint can appear between the readers.
		r.CheckedAt = 0
		out[billingRowID(r.Source, r.Provider, r.Model, r.UpstreamModel)] = r
	}
	return mustJSON(out)
}

func (s *Service) warmSpendSlice(ctx context.Context, job spendSliceJob) error {
	var epoch int64
	var room bool
	if err := s.engine.DB.QueryRowContext(ctx, `SELECT epoch,pg_database_size(current_database())+67108864<ingest_limit_bytes FROM channel_spend_epoch,storage_state
 WHERE channel_spend_epoch.singleton AND storage_state.singleton`).Scan(&epoch, &room); err != nil || !room {
		return err
	}
	capture := newSpendCapture(true)
	rows, err := s.readAttributedChannelSpend(ctx, job.owner, job.filter, job.from, job.to, capture)
	if err != nil {
		return err
	}
	if capture.unsafe || capture.binding != job.binding || len(capture.identities) > 40000 {
		return nil
	}
	for _, r := range rows {
		if r.Pending > 0 || r.AbsentReceipts > 0 || r.SyncError {
			return nil
		}
	}
	payload, err := json.Marshal(slicePayload(rows, capture))
	if err != nil {
		return err
	}
	bytes := len(payload)
	for _, keys := range [][]string{capture.identities, capture.claims, capture.receipts, capture.evidence} {
		for _, key := range keys {
			bytes += len(key) + 16
		}
	}
	if bytes > 1<<20 {
		return nil
	}
	if capture.identities == nil {
		capture.identities = []string{}
	}
	if capture.claims == nil {
		capture.claims = []string{}
	}
	if capture.receipts == nil {
		capture.receipts = []string{}
	}
	if capture.evidence == nil {
		capture.evidence = []string{}
	}
	tx, err := s.engine.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current int64
	if err = tx.QueryRowContext(ctx, `SELECT epoch FROM channel_spend_epoch WHERE singleton FOR UPDATE`).Scan(&current); err != nil || current != epoch {
		return err
	}
	// Bound the live working set without treating free/allocated disk pages as
	// live cache. Relational pages are reused by autovacuum after eviction.
	if _, err = tx.ExecContext(ctx, `DELETE FROM channel_spend_slices WHERE (scope,from_sec,to_sec) IN (
 SELECT scope,from_sec,to_sec FROM (SELECT scope,from_sec,to_sec,
   sum(size_bytes) OVER w AS used,row_number() OVER w AS n FROM channel_spend_slices
   WINDOW w AS (ORDER BY saved_at DESC,scope,from_sec,to_sec)) ranked WHERE used>? OR n>=2048)`, (24<<20)-bytes); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO channel_spend_slices(scope,from_sec,to_sec,binding,payload,identities,claims,receipts,evidence,size_bytes)
 VALUES(?,?,?,?,?::jsonb,?,?,?,?,?) ON CONFLICT(scope,from_sec,to_sec) DO UPDATE SET binding=excluded.binding,payload=excluded.payload,
 identities=excluded.identities,claims=excluded.claims,receipts=excluded.receipts,evidence=excluded.evidence,size_bytes=excluded.size_bytes,saved_at=now()`, job.scope, job.from, job.to, job.binding, string(payload), capture.identities, capture.claims, capture.receipts, capture.evidence, bytes)
	if err != nil {
		return err
	}
	if err = tx.Commit(); err == nil {
		log.Printf("event=channel_spend_slice_ready seconds=%d rows=%d", job.to-job.from, len(rows))
	}
	return err
}
