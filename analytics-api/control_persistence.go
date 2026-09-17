package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

const controlPersistenceSchema = `CREATE TABLE IF NOT EXISTS console_control_snapshots (
 source_id TEXT PRIMARY KEY REFERENCES console_sources(id) ON DELETE CASCADE, enabled BOOLEAN NOT NULL DEFAULT true,
 encrypted_snapshot TEXT NOT NULL DEFAULT '', target_hash TEXT NOT NULL DEFAULT '',
 instance_id TEXT NOT NULL DEFAULT '', revision TEXT NOT NULL DEFAULT '',
 restoring_instance TEXT NOT NULL DEFAULT '', restore_revision TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL DEFAULT 'pending', message TEXT NOT NULL DEFAULT '',
 saved_at TIMESTAMPTZ, restored_at TIMESTAMPTZ, checked_at TIMESTAMPTZ,
 rule_count INTEGER NOT NULL DEFAULT 0, channel_count INTEGER NOT NULL DEFAULT 0);`

type retainedRule struct {
	KeyID    string   `json:"api_key_id"`
	Model    string   `json:"model"`
	Order    []string `json:"order"`
	Disabled []string `json:"disabled"`
}
type retainedChannel struct {
	Provider string   `json:"provider"`
	KeyID    string   `json:"api_key_id"`
	Base     string   `json:"base_url"`
	Key      string   `json:"api_key"`
	Models   []string `json:"models"`
}
type retainedSnapshot struct {
	Version  int               `json:"version"`
	Rules    []retainedRule    `json:"rules"`
	Channels []retainedChannel `json:"temporary_channels"`
}
type retainedLive struct {
	Instance string            `json:"instance_id"`
	Revision string            `json:"revision"`
	Atomic   bool              `json:"temporary_channel_restore"`
	Rules    []retainedRule    `json:"rules"`
	Channels []retainedChannel `json:"temporary_channels"`
}
type retainedRecord struct {
	Enabled                                                           bool
	Encrypted, Target, Instance, Revision, Restoring, RestoreRevision string
}

// One session lock per source serializes UI writes and recovery across replicas.
// Model-serving traffic never uses this lock or this PostgreSQL connection.
func (s *controlStore) lockControls(ctx context.Context, source string) (func(), error) {
	c, e := s.db.Conn(ctx)
	if e != nil {
		return nil, e
	}
	sum := sha256.Sum256([]byte("console-controls:" + source))
	id := int64(binary.BigEndian.Uint64(sum[:8]))
	var ok bool
	if e = c.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, id).Scan(&ok); e != nil || !ok {
		c.Close()
		return nil, errors.New("渠道配置正在同步，请稍后重试")
	}
	return func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, err := c.ExecContext(cleanup, `SELECT pg_advisory_unlock($1)`, id)
		if err != nil {
			_ = c.Raw(func(any) error { return driver.ErrBadConn })
		}
		c.Close()
	}, nil
}
func (s *controlStore) retainedRecord(ctx context.Context, source string) (retainedRecord, error) {
	var r retainedRecord
	_, e := s.db.ExecContext(ctx, `INSERT INTO console_control_snapshots(source_id) VALUES($1) ON CONFLICT DO NOTHING`, source)
	if e != nil {
		return r, e
	}
	e = s.db.QueryRowContext(ctx, `SELECT enabled,encrypted_snapshot,target_hash,instance_id,revision,restoring_instance,restore_revision FROM console_control_snapshots WHERE source_id=$1`, source).Scan(&r.Enabled, &r.Encrypted, &r.Target, &r.Instance, &r.Revision, &r.Restoring, &r.RestoreRevision)
	return r, e
}
func controlTarget(src controlSource) string { return tokenHash(src.Base + "\x00" + src.Key) }
func decodeRetained(raw map[string]any) (retainedLive, error) {
	var l retainedLive
	e := decodeMap(raw, &l)
	if e == nil && (l.Instance == "" || l.Revision == "") {
		e = errors.New("来源未提供实例标识，无法可靠恢复配置")
	}
	return l, e
}
func (s *controlStore) retainedSnapshot(r retainedRecord) (retainedSnapshot, error) {
	var v retainedSnapshot
	raw, e := s.decrypt(r.Encrypted)
	if e != nil {
		return v, e
	}
	e = json.Unmarshal([]byte(raw), &v)
	if e == nil && v.Version != 1 {
		e = errors.New("unsupported snapshot")
	}
	return v, e
}
func (s *Service) captureControls(ctx context.Context, src controlSource, live retainedLive, old retainedRecord) error {
	if !old.Enabled {
		return nil
	}
	known := map[string]retainedChannel{}
	if old.Encrypted != "" && old.Target == controlTarget(src) {
		v, e := s.control.retainedSnapshot(old)
		if e != nil {
			return e
		}
		for _, c := range v.Channels {
			known[c.Provider] = c
		}
	}
	// Existing gateways redact channel secrets. Recover only the exact business
	// credentials created by this console, never a test key or another group.
	type credential struct {
		account, base, key string
		group              int64
	}
	credentials := []credential{}
	if len(live.Channels) > 0 {
		rows, e := s.control.db.QueryContext(ctx, `SELECT a.id,a.base,t.group_id,t.encrypted_routing_key FROM console_sub_accounts a JOIN console_sub_targets t ON t.account_id=a.id WHERE t.encrypted_routing_key<>''`)
		if e != nil {
			return e
		}
		for rows.Next() {
			var c credential
			var enc string
			if e = rows.Scan(&c.account, &c.base, &c.group, &enc); e != nil {
				rows.Close()
				return e
			}
			c.key, e = s.control.decrypt(enc)
			if e != nil {
				rows.Close()
				return e
			}
			credentials = append(credentials, c)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
	}
	snapshot := retainedSnapshot{Version: 1, Rules: live.Rules, Channels: []retainedChannel{}}
	for _, p := range live.Channels {
		saved, ok := known[p.Provider]
		if !ok || saved.KeyID != p.KeyID {
			ok = false
			for _, c := range credentials {
				if subProviderName(c.account, c.group, p.KeyID) == p.Provider {
					saved = retainedChannel{Provider: p.Provider, KeyID: p.KeyID, Base: c.base + "/v1/responses", Key: c.key}
					ok = true
					break
				}
			}
		}
		if !ok || saved.Key == "" {
			return errors.New("有临时渠道缺少可恢复凭据，保留原备份；请勿重启来源")
		}
		saved.Models = append([]string{}, p.Models...)
		snapshot.Channels = append(snapshot.Channels, saved)
	}
	raw, e := json.Marshal(snapshot)
	if e != nil {
		return e
	}
	enc, e := s.control.encrypt(string(raw))
	if e != nil {
		return e
	}
	_, e = s.control.db.ExecContext(ctx, `UPDATE console_control_snapshots SET encrypted_snapshot=$2,target_hash=$3,instance_id=$4,revision=$5,restoring_instance='',restore_revision='',status='saved',message='',saved_at=now(),checked_at=now(),rule_count=$6,channel_count=$7 WHERE source_id=$1 AND enabled`, src.ID, enc, controlTarget(src), live.Instance, live.Revision, len(snapshot.Rules), len(snapshot.Channels))
	return e
}
func controlEquivalent(live retainedLive, saved retainedSnapshot) bool {
	scrub := func(v retainedSnapshot) string {
		for i := range v.Channels {
			v.Channels[i].Key = ""
			v.Channels[i].Base = ""
			v.Channels[i].Models = append([]string{}, v.Channels[i].Models...)
			sort.Strings(v.Channels[i].Models)
		}
		sort.Slice(v.Channels, func(i, j int) bool { return v.Channels[i].Provider < v.Channels[j].Provider })
		sort.Slice(v.Rules, func(i, j int) bool {
			return v.Rules[i].KeyID+"\x00"+v.Rules[i].Model < v.Rules[j].KeyID+"\x00"+v.Rules[j].Model
		})
		b, _ := json.Marshal(v)
		return string(b)
	}
	// Normalize nil lists as empty, without mutating saved secrets.
	clone := func(v retainedSnapshot) retainedSnapshot {
		b, _ := json.Marshal(v)
		var c retainedSnapshot
		_ = json.Unmarshal(b, &c)
		if c.Rules == nil {
			c.Rules = []retainedRule{}
		}
		if c.Channels == nil {
			c.Channels = []retainedChannel{}
		}
		for i := range c.Rules {
			if c.Rules[i].Order == nil {
				c.Rules[i].Order = []string{}
			}
			if c.Rules[i].Disabled == nil {
				c.Rules[i].Disabled = []string{}
			}
		}
		return c
	}
	return scrub(clone(retainedSnapshot{Version: 1, Rules: live.Rules, Channels: live.Channels})) == scrub(clone(saved))
}

// Caller holds the source lock. A new empty instance must never replace a
// nonempty saved intent. Conflicting live changes stop restoration visibly.
func (s *Service) reconcileControls(ctx context.Context, src controlSource) (map[string]any, error) {
	record, e := s.control.retainedRecord(ctx, src.ID)
	if e != nil {
		return nil, e
	}
	raw, _, e := subGateway(ctx, src, "GET", "/v1/channel-controls", nil)
	if e != nil {
		return nil, e
	}
	if !record.Enabled {
		return raw, nil
	}
	live, e := decodeRetained(raw)
	if e != nil {
		return nil, e
	}
	if record.Encrypted == "" || record.Target != controlTarget(src) || record.Instance == live.Instance && record.Restoring == "" {
		if record.Revision != live.Revision || record.Target != controlTarget(src) {
			e = s.captureControls(ctx, src, live, record)
		} else {
			_, e = s.control.db.ExecContext(ctx, `UPDATE console_control_snapshots SET status='saved',message='',checked_at=now() WHERE source_id=$1 AND status<>'saved'`, src.ID)
		}
		return raw, e
	}
	saved, e := s.control.retainedSnapshot(record)
	if e != nil {
		return nil, e
	}
	if controlEquivalent(live, saved) {
		_, e = s.control.db.ExecContext(ctx, `UPDATE console_control_snapshots SET instance_id=$2,revision=$3,restoring_instance='',restore_revision='',status='saved',message='',restored_at=now(),checked_at=now() WHERE source_id=$1`, src.ID, live.Instance, live.Revision)
		return raw, e
	}
	continuing := record.Restoring == live.Instance && record.RestoreRevision == live.Revision
	if !continuing && (len(live.Rules) > 0 || len(live.Channels) > 0) {
		return nil, errors.New("新实例已有不同的临时修改，自动恢复已暂停以避免覆盖")
	}
	_, e = s.control.db.ExecContext(ctx, `UPDATE console_control_snapshots SET restoring_instance=$2,restore_revision=$3,status='restoring',message='',checked_at=now() WHERE source_id=$1`, src.ID, live.Instance, live.Revision)
	if e != nil {
		return nil, e
	}
	apply := func(path string, body map[string]any) error {
		body["revision"] = live.Revision
		next, _, err := subGateway(ctx, src, "POST", path, body)
		if err != nil {
			return err
		}
		latest, err := decodeRetained(next)
		if err != nil {
			return err
		}
		if latest.Instance != live.Instance {
			return errors.New("恢复期间实例再次变化，等待重新核对")
		}
		live = latest
		raw = next
		_, err = s.control.db.ExecContext(ctx, `UPDATE console_control_snapshots SET restore_revision=$2 WHERE source_id=$1`, src.ID, live.Revision)
		return err
	}
	if live.Atomic {
		if e = apply("/v1/channel-controls/restore", map[string]any{"snapshot": saved}); e != nil {
			return nil, e
		}
	} else {
		// Compatibility path for deployed gateways predating atomic restoration.
		// Each acknowledged step is revision-fenced; retry resumes only our exact state.
		for _, p := range saved.Channels {
			if e = apply("/v1/temporary-channels", map[string]any{"api_key_id": p.KeyID, "provider": p.Provider, "base_url": p.Base, "api_key": p.Key, "models": p.Models, "position": 1}); e != nil {
				return nil, e
			}
		}
		for _, r := range live.Rules {
			found := false
			for _, v := range saved.Rules {
				if v.KeyID == r.KeyID && v.Model == r.Model {
					found = true
					break
				}
			}
			if !found {
				if e = apply("/v1/channel-controls", map[string]any{"action": "set", "api_key_id": r.KeyID, "model": r.Model, "order": []string{}, "disabled": []string{}}); e != nil {
					return nil, e
				}
			}
		}
		for _, r := range saved.Rules {
			if e = apply("/v1/channel-controls", map[string]any{"action": "set", "api_key_id": r.KeyID, "model": r.Model, "order": r.Order, "disabled": r.Disabled}); e != nil {
				return nil, e
			}
		}
	}
	if !controlEquivalent(live, saved) {
		return nil, errors.New("恢复后的配置校验不一致，保留备份并暂停")
	}
	_, e = s.control.db.ExecContext(ctx, `UPDATE console_control_snapshots SET instance_id=$2,revision=$3,restoring_instance='',restore_revision='',status='saved',message='',restored_at=now(),checked_at=now() WHERE source_id=$1`, src.ID, live.Instance, live.Revision)
	return raw, e
}
func (s *Service) controlRecoveryLoop(ctx context.Context) {
	for ctx.Err() == nil {
		sources, e := s.control.listSources(ctx)
		if e == nil {
			for _, source := range sources {
				func() {
					task, cancel := context.WithTimeout(ctx, 90*time.Second)
					defer cancel()
					unlock, err := s.control.lockControls(task, source.ID)
					if err != nil {
						return
					}
					defer unlock()
					src, err := s.control.source(task, source.ID)
					if err != nil {
						return
					}
					_, err = s.reconcileControls(task, src)
					if err != nil {
						_, _ = s.control.db.ExecContext(task, `UPDATE console_control_snapshots SET status='error',message=$2,checked_at=now() WHERE source_id=$1`, src.ID, err.Error())
					}
				}()
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}
func (s *Service) controlPersistence(w http.ResponseWriter, r *http.Request) {
	src, e := s.control.source(r.Context(), r.PathValue("id"))
	if e != nil {
		http.Error(w, "来源不存在", 404)
		return
	}
	if r.Method == "PUT" {
		var in struct {
			Enabled *bool `json:"enabled"`
		}
		if !decodeControl(w, r, &in) {
			return
		}
		if in.Enabled == nil {
			http.Error(w, "enabled required", 400)
			return
		}
		unlock, err := s.control.lockControls(r.Context(), src.ID)
		if err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		defer unlock()
		before, err := s.control.retainedRecord(r.Context(), src.ID)
		if err != nil {
			http.Error(w, "配置存储暂不可用", 503)
			return
		}
		// Enabling starts with current state, never revives a disabled old snapshot.
		if before.Enabled != *in.Enabled {
			_, err = s.control.db.ExecContext(r.Context(), `UPDATE console_control_snapshots SET enabled=$2,encrypted_snapshot='',instance_id='',revision='',restoring_instance='',restore_revision='',status=CASE WHEN $2 THEN 'pending' ELSE 'disabled' END,message='',rule_count=0,channel_count=0 WHERE source_id=$1`, src.ID, *in.Enabled)
		}
		if err != nil {
			http.Error(w, "设置保存失败", 503)
			return
		}
		if *in.Enabled {
			if _, err = s.reconcileControls(r.Context(), src); err != nil {
				http.Error(w, "已开启，但当前配置保存失败："+err.Error(), 503)
				return
			}
		}
	}
	_, e = s.control.retainedRecord(r.Context(), src.ID)
	if e != nil {
		http.Error(w, "配置存储暂不可用", 503)
		return
	}
	var enabled bool
	var status, message string
	var rules, channels int
	var saved, restored, checked sql.NullInt64
	e = s.control.db.QueryRowContext(r.Context(), `SELECT enabled,status,message,rule_count,channel_count,extract(epoch FROM saved_at)::bigint,extract(epoch FROM restored_at)::bigint,extract(epoch FROM checked_at)::bigint FROM console_control_snapshots WHERE source_id=$1`, src.ID).Scan(&enabled, &status, &message, &rules, &channels, &saved, &restored, &checked)
	if e != nil {
		http.Error(w, "状态读取失败", 503)
		return
	}
	stamp := func(v sql.NullInt64) any {
		if v.Valid {
			return v.Int64
		}
		return nil
	}
	writeJSON(w, 200, map[string]any{"enabled": enabled, "status": status, "message": message, "rules": rules, "channels": channels, "saved_at": stamp(saved), "restored_at": stamp(restored), "checked_at": stamp(checked)})
}
func (s *Service) saveLiveControls(ctx context.Context, src controlSource, raw map[string]any) error {
	r, e := s.control.retainedRecord(ctx, src.ID)
	if e != nil || !r.Enabled {
		return e
	}
	live, e := decodeRetained(raw)
	if e != nil {
		return e
	}
	return s.captureControls(ctx, src, live, r)
}
func retentionFailure(w http.ResponseWriter, err error) {
	http.Error(w, fmt.Sprintf("修改已应用，但保存恢复配置失败：%s。请勿重启来源，刷新后核对。", err), 503)
}

// Authenticated by the source administrator credential, not a browser session.
// Only the booting source receives its own encrypted-at-rest routing intent.
func (s *Service) bootstrapControls(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.control == nil {
		http.Error(w, "unavailable", 503)
		return
	}
	src, e := s.control.source(r.Context(), r.PathValue("id"))
	if e != nil {
		http.Error(w, "unauthorized", 401)
		return
	}
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		http.Error(w, "unauthorized", 401)
		return
	}
	supplied := strings.TrimPrefix(header, "Bearer ")
	a, b := sha256.Sum256([]byte(supplied)), sha256.Sum256([]byte(src.Key))
	if subtle.ConstantTimeCompare(a[:], b[:]) != 1 {
		http.Error(w, "unauthorized", 401)
		return
	}
	record, e := s.control.retainedRecord(r.Context(), src.ID)
	if e != nil {
		http.Error(w, "configuration storage unavailable", 503)
		return
	}
	if !record.Enabled {
		writeJSON(w, 200, map[string]any{"enabled": false})
		return
	}
	if record.Encrypted == "" || record.Target != controlTarget(src) {
		http.Error(w, "configuration snapshot not ready", 503)
		return
	}
	saved, e := s.control.retainedSnapshot(record)
	if e != nil {
		http.Error(w, "configuration snapshot unavailable", 503)
		return
	}
	writeJSON(w, 200, map[string]any{"enabled": true, "snapshot": saved})
}
