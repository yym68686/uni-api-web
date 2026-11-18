package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log"
	"slices"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	managementDisplayRawLimit    = 2 << 20
	managementDisplayPackedLimit = 512 << 10
	managementDisplayStoreLimit  = 48 << 20
	managementDisplayReadBatch   = 25
	managementDisplayWarmBatch   = 8
)

const managementDisplayEpochJoins = `
 LEFT JOIN console_management_display_epochs m ON m.kind='model' AND m.account_id=w.account_id AND m.group_id=w.group_id
 LEFT JOIN console_management_display_epochs u ON u.kind='receipt' AND u.account_id=w.account_id AND u.group_id=w.group_id
 CROSS JOIN LATERAL (SELECT concat_ws(':','summary-v1',COALESCE(m.writer::text,''),COALESCE(u.writer::text,''),
 COALESCE((SELECT writer::text FROM console_management_display_epochs WHERE kind='reset' AND account_id='' AND group_id=0),'')) AS version) epoch`

type managementDisplayContextKey struct{}

type managementDisplayScope struct {
	tx             *sql.Tx
	owner, version string
}

func withManagementDisplayVersion(ctx context.Context, tx *sql.Tx, owner, version string) context.Context {
	if tx == nil || owner == "" || version == "" {
		return ctx
	}
	return context.WithValue(ctx, managementDisplayContextKey{}, managementDisplayScope{tx, owner, version})
}

type managementDisplayGroup struct {
	bound        subBoundKey
	localVersion string
	size         int
	body         []byte
}

type managementDisplayPayload struct {
	Account string           `json:"a"`
	Group   int64            `json:"g"`
	Models  []subModelResult `json:"m"`
}

// The representation contains only normalized list model summaries. It does
// not preserve probe IDs, raw receipts, credentials or an authorization grant.
func packManagementDisplayGroup(bound subBoundKey, models []subModelResult) (managementDisplayGroup, bool) {
	raw, err := json.Marshal(managementDisplayPayload{bound.AccountID, bound.GroupID, models})
	if err != nil || len(raw) < 2 || len(raw) > managementDisplayRawLimit {
		return managementDisplayGroup{}, false
	}
	var out bytes.Buffer
	w, err := gzip.NewWriterLevel(&out, gzip.BestSpeed)
	if err != nil {
		return managementDisplayGroup{}, false
	}
	_, err = w.Write(raw)
	closeErr := w.Close()
	if err != nil || closeErr != nil || out.Len() > managementDisplayPackedLimit {
		return managementDisplayGroup{}, false
	}
	return managementDisplayGroup{bound: bound, size: len(raw), body: bytes.Clone(out.Bytes())}, true
}

func (g managementDisplayGroup) models(ctx context.Context) ([]subModelResult, bool) {
	if g.size < 2 || g.size > managementDisplayRawLimit || len(g.body) < 1 || len(g.body) > managementDisplayPackedLimit {
		return nil, false
	}
	reader, err := gzip.NewReader(bytes.NewReader(g.body))
	if err != nil {
		return nil, false
	}
	defer reader.Close()
	data := make([]byte, g.size)
	if _, err = io.ReadFull(reader, data); err != nil {
		return nil, false
	}
	var extra [1]byte
	if n, err := io.ReadFull(reader, extra[:]); n != 0 || err != io.EOF || ctx.Err() != nil {
		return nil, false
	}
	var payload managementDisplayPayload
	if err := json.Unmarshal(data, &payload); err != nil || payload.Models == nil ||
		payload.Account != g.bound.AccountID || payload.Group != g.bound.GroupID {
		return nil, false
	}
	return payload.Models, true
}

// Fetch only the requested groups, from the transaction which proved their
// exact source epochs. This is a derived-data lookup, not a fresh-evidence
// check in its own right. A damaged or missing entry is never a partial hit.
func readManagementDisplayGroups(ctx context.Context, db controlQueryDB, owner string, wanted []subBoundKey) (map[string][]subModelResult, error) {
	scope, ok := ctx.Value(managementDisplayContextKey{}).(managementDisplayScope)
	if tx, pinned := db.(*sql.Tx); !ok || !pinned || tx != scope.tx || owner != scope.owner || len(wanted) == 0 {
		return nil, nil
	}
	end := managementReadSpan(ctx, "hydrate_display_groups")
	defer end()
	out := make(map[string][]subModelResult, len(wanted))
	for start := 0; start < len(wanted); start += managementDisplayReadBatch {
		batch := wanted[start:min(start+managementDisplayReadBatch, len(wanted))]
		accounts, groups := managementDisplayIDs(batch)
		rows, err := db.QueryContext(ctx, `SELECT e.account_id,e.group_id,e.raw_bytes,e.body
 FROM unnest($3::text[],$4::bigint[]) AS w(account_id,group_id)
 JOIN console_management_display_groups e USING (account_id,group_id)
 JOIN console_sub_accounts a ON a.id=w.account_id`+managementDisplayEpochJoins+`
 WHERE e.owner_key=$2 AND a.owner=$1
 AND (SELECT complete FROM console_management_projection_state WHERE singleton)
 AND (e.local_version=epoch.version OR (e.local_version='' AND e.version=$5))`, owner, tokenHash(owner), accounts, groups, scope.version)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var g managementDisplayGroup
			if err = rows.Scan(&g.bound.AccountID, &g.bound.GroupID, &g.size, &g.body); err != nil {
				rows.Close()
				return nil, err
			}
			if models, valid := g.models(ctx); valid {
				out[qualityGroupID(g.bound.AccountID, g.bound.GroupID)] = models
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	if managementTraceEnabled(ctx) {
		log.Printf("management_display_groups hit=%d wanted=%d request_id=%s", len(out), len(wanted), consoleRequestID(ctx))
	}
	return out, ctx.Err()
}

func managementDisplayIDs(wanted []subBoundKey) ([]string, []int64) {
	accounts := make([]string, len(wanted))
	groups := make([]int64, len(wanted))
	for i, g := range wanted {
		accounts[i], groups[i] = g.AccountID, g.GroupID
	}
	return accounts, groups
}

// One short, serialized writer, outside the request transaction. Old work
// cannot replace newer work. A missing budget, insufficient headroom or
// contention skips this disposable cache; no ingestion threshold is changed.
func (s *Service) storeManagementDisplayGroups(ctx context.Context, owner, version string, seenAt time.Time, groups []managementDisplayGroup) (bool, error) {
	if len(groups) == 0 {
		return false, nil
	}
	tx, err := s.control.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SET LOCAL lock_timeout='100ms'; SET LOCAL statement_timeout='1s'`); err != nil {
		return false, err
	}
	var locked bool
	if err = tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock(8395042)`).Scan(&locked); err != nil || !locked {
		return false, err
	}
	var add int64
	wanted := make([]subBoundKey, len(groups))
	sizes := make([]int32, len(groups))
	bodies := make([][]byte, len(groups))
	locals := make([]string, len(groups))
	for i, g := range groups {
		wanted[i], sizes[i], bodies[i] = g.bound, int32(g.size), g.body
		locals[i] = g.localVersion
		add += int64(len(g.body))
	}
	accountIDs, groupIDs := managementDisplayIDs(wanted)
	budget := pgx.Identifier{s.engine.historyPath, "storage_state"}.Sanitize()
	var room bool
	err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT pg_database_size(current_database())+33554432+$1::bigint*4 < ingest_limit_bytes FROM `+budget+` WHERE singleton),false)
 AND (SELECT COALESCE(sum(octet_length(body)),0)+$1-(SELECT COALESCE(sum(octet_length(e.body)),0)
 FROM unnest($3::text[],$4::bigint[]) w(account_id,group_id) JOIN console_management_display_groups e USING(account_id,group_id)
 WHERE e.owner_key=$5) <= $2 FROM console_management_display_groups)`, add, int64(managementDisplayStoreLimit), accountIDs, groupIDs, tokenHash(owner)).Scan(&room)
	if err != nil || !room {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO console_management_display_groups(owner_key,account_id,group_id,version,local_version,observed_at,raw_bytes,body)
 SELECT $1,w.account_id,w.group_id,$2,w.local_version,$3,w.raw_bytes,w.body
 FROM unnest($4::text[],$5::bigint[],$6::int[],$7::bytea[],$9::text[]) AS w(account_id,group_id,raw_bytes,body,local_version)
 JOIN console_sub_accounts a ON a.id=w.account_id AND a.owner=$8
 JOIN console_sub_targets t ON t.account_id=w.account_id AND t.group_id=w.group_id`+managementDisplayEpochJoins+`
 WHERE w.local_version='' OR w.local_version=epoch.version
 ON CONFLICT(owner_key,account_id,group_id) DO UPDATE SET version=EXCLUDED.version,local_version=EXCLUDED.local_version,observed_at=EXCLUDED.observed_at,raw_bytes=EXCLUDED.raw_bytes,body=EXCLUDED.body
 WHERE console_management_display_groups.observed_at <= EXCLUDED.observed_at`, tokenHash(owner), version, seenAt, accountIDs, groupIDs, sizes, bodies, owner, locals)
	if err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// Warm a bounded batch from a pinned, owner-scoped snapshot. Readers still
// check the exact current version, including writes committed after this read.
// This worker neither calls the gateway nor synthesizes application sessions.
func (s *Service) warmManagementDisplayGroups(ctx context.Context, owner string, after subBoundKey) (subBoundKey, int, bool, error) {
	var version string
	var seen time.Time
	var last subBoundKey
	compiled := []managementDisplayGroup{}
	err := func() error {
		tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			return err
		}
		defer tx.Rollback()
		directory := &managementDirectoryScope{store: &s.modelDirectories, database: s.control.db, tx: tx, owner: owner}
		child := context.WithValue(ctx, managementDirectoryScopeKey{}, directory)
		proof, now, err := s.managementReadProof(child, tx, owner)
		if err != nil || proof.DisplayVersion == "" {
			return err
		}
		version, seen = proof.DisplayVersion, now
		rows, err := tx.QueryContext(ctx, `SELECT w.account_id,w.group_id,epoch.version FROM console_sub_targets w
 JOIN console_sub_accounts a ON a.id=w.account_id
 LEFT JOIN console_management_display_groups e ON e.owner_key=$2 AND e.account_id=w.account_id AND e.group_id=w.group_id`+managementDisplayEpochJoins+`
 WHERE a.owner=$1 AND (w.account_id,w.group_id)>($4,$5)
 AND (SELECT complete FROM console_management_projection_state WHERE singleton)
 AND (e.account_id IS NULL OR e.observed_at<transaction_timestamp()-interval '24 hours' OR NOT
 (e.local_version=epoch.version OR (e.local_version='' AND e.version=$3)))
 ORDER BY w.account_id,w.group_id LIMIT $6`, owner, tokenHash(owner), version, after.AccountID, after.GroupID, managementDisplayWarmBatch)
		if err != nil {
			return err
		}
		wanted := []subBoundKey{}
		epochs := []string{}
		for rows.Next() {
			var key subBoundKey
			var epoch string
			if err := rows.Scan(&key.AccountID, &key.GroupID, &epoch); err != nil {
				rows.Close()
				return err
			}
			wanted = append(wanted, key)
			epochs = append(epochs, epoch)
		}
		err = rows.Err()
		rows.Close()
		if err != nil || len(wanted) == 0 {
			return err
		}
		last = wanted[len(wanted)-1]
		directory.accountRevision = proof.Epochs["console_sub_accounts"]
		directory.directory = s.modelDirectories.get(managementDirectoryVersion{s.control.db, owner, directory.accountRevision, directory.modelRevision})
		slots := make(map[string]*subTarget, len(wanted))
		subset := make([]subAccount, len(wanted))
		for i, g := range wanted {
			subset[i] = subAccount{ID: g.AccountID, Targets: []subTarget{{GroupID: g.GroupID, Models: []subModelResult{}}}}
			slots[qualityGroupID(g.AccountID, g.GroupID)] = &subset[i].Targets[0]
		}
		err = readManagementPageModelSQL(child, tx, owner, wanted, func(row managementPageModelRecord, _ *managementRowsTrace) error {
			return appendManagementDisplayModel(slots, row)
		})
		if err != nil {
			return err
		}
		local, err := managementDisplayLocalReceipts(child, tx, owner, subset)
		if err != nil {
			return err
		}
		if err = s.attachManagementPageSummaryUsageFrom(child, tx, owner, subset, nil); err != nil {
			return err
		}
		for i, g := range wanted {
			models := subset[i].Targets[0].Models
			sort.Slice(models, func(a, b int) bool { return models[a].Model < models[b].Model })
			normalizeManagementModels(models)
			if pack, ok := packManagementDisplayGroup(g, models); ok {
				if local[i] {
					pack.localVersion = epochs[i]
				}
				compiled = append(compiled, pack)
			}
		}
		return ctx.Err()
	}()
	if err != nil || len(compiled) == 0 {
		return last, 0, false, err
	}
	stored, err := s.storeManagementDisplayGroups(ctx, owner, version, seen, compiled)
	return last, len(compiled), stored, err
}

// A missing, cross-group or inconsistent receipt keeps the broad proof.
// In particular, a later insert elsewhere must invalidate a formerly absent ID
// regardless of the source group epoch.
func managementDisplayLocalReceipts(ctx context.Context, tx *sql.Tx, owner string, subset []subAccount) ([]bool, error) {
	deps := make([]map[string][]*subProbe, len(subset))
	requested := map[string]bool{}
	for i := range subset {
		deps[i] = subUsageProbes(subset[i : i+1])
		for id := range deps[i] {
			requested[id] = true
		}
	}
	ids := make([]string, 0, len(requested))
	for id := range requested {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	owners := make(map[string]managementPageModelGroup, len(ids))
	for start := 0; start < len(ids); start += 4096 {
		rows, err := tx.QueryContext(ctx, `SELECT r.id,r.account_id,u.group_id FROM console_management_receipt_signal r
 JOIN console_sub_usage u ON u.id=r.id AND u.account_id=r.account_id
 JOIN console_sub_accounts a ON a.id=r.account_id WHERE a.owner=$1 AND r.valid AND r.id=ANY($2)`, owner, ids[start:min(start+4096, len(ids))])
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			var group managementPageModelGroup
			if err := rows.Scan(&id, &group.account, &group.group); err != nil {
				rows.Close()
				return nil, err
			}
			owners[id] = group
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	result := make([]bool, len(subset))
	for i, probes := range deps {
		result[i] = true
		group := managementPageModelGroup{subset[i].ID, subset[i].Targets[0].GroupID}
		for id := range probes {
			if owners[id] != group {
				result[i] = false
				break
			}
		}
	}
	return result, ctx.Err()
}

func appendManagementDisplayModel(slots map[string]*subTarget, row managementPageModelRecord) error {
	target := slots[qualityGroupID(row.account, row.group)]
	model := subModelResult{Model: row.model, State: row.state, Message: row.message}
	if len(row.result) > 0 {
		if err := json.Unmarshal(row.result, &model.Result); err != nil {
			return err
		}
	}
	compactSubModelResult(model.Result)
	if target != nil {
		target.Models = append(target.Models, model)
	}
	return nil
}

func (s *Service) managementDisplayLoop(ctx context.Context) {
	if s.engine == nil {
		return
	}
	// Do not make readiness depend on this disposable projection. One small
	// batch at a time leaves room for live reads and other control workers.
	positions := map[string]subBoundKey{}
	lastOwner := ""
	for waitStartup(ctx, 5*time.Second) {
		if s.managementReaders.Load() != 0 {
			continue
		}
		func() {
			step, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			var owner string
			err := s.control.db.QueryRowContext(step, `SELECT username FROM (
 SELECT DISTINCT s.username FROM console_sessions s
 WHERE s.expires_at>now() AND EXISTS(SELECT 1 FROM console_sub_accounts a WHERE a.owner=s.username)
 ) active ORDER BY (username <= $1),username LIMIT 1`, lastOwner).Scan(&owner)
			if err != nil {
				return
			}
			lastOwner = owner
			// Keep cursor metadata bounded even if sessions switch frequently.
			if len(positions) >= 16 && positions[owner] == (subBoundKey{}) {
				clear(positions)
			}
			last, n, stored, err := s.warmManagementDisplayGroups(step, owner, positions[owner])
			if err != nil {
				log.Printf("management_display_warm class=%s", databaseErrorClass(err))
				return
			}
			if last != (subBoundKey{}) {
				positions[owner] = last
			} else {
				delete(positions, owner)
			}
			if n > 0 {
				log.Printf("management_display_warm groups=%d stored=%t", n, stored)
			}
		}()
	}
}
