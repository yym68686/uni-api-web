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
 rule_count INTEGER NOT NULL DEFAULT 0, channel_count INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS console_control_bootstraps (
 source_id TEXT NOT NULL REFERENCES console_sources(id) ON DELETE CASCADE,
 target_hash TEXT NOT NULL, snapshot_id TEXT NOT NULL, encrypted_snapshot TEXT NOT NULL,
 instance_id TEXT NOT NULL, revision TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(source_id,target_hash,snapshot_id));
CREATE TABLE IF NOT EXISTS console_control_retired_instances (
 source_id TEXT NOT NULL REFERENCES console_sources(id) ON DELETE CASCADE,
 target_hash TEXT NOT NULL, instance_id TEXT NOT NULL, superseded_by TEXT NOT NULL,
 retired_at TIMESTAMPTZ NOT NULL DEFAULT now(), PRIMARY KEY(source_id,target_hash,instance_id));`

type retainedRule struct {
	KeyID    string   `json:"api_key_id"`
	Model    string   `json:"model"`
	Order    []string `json:"order"`
	Disabled []string `json:"disabled"`
}
type retainedChannel struct {
	Definition json.RawMessage `json:"definition,omitempty"`
	Provider   string          `json:"provider"`
	KeyID      string          `json:"api_key_id"`
	Base       string          `json:"base_url"`
	Key        string          `json:"api_key"`
	Models     []string        `json:"models"`
}
type retainedSnapshot struct {
	Settings map[string]json.RawMessage `json:"channel_settings,omitempty"`
	Version  int                        `json:"version"`
	Rules    []retainedRule             `json:"rules"`
	Channels []retainedChannel          `json:"temporary_channels"`
}
type retainedLive struct {
	Bootstrap         *bootstrapReceipt `json:"bootstrap_restore,omitempty"`
	DefinitionsDigest string            `json:"channel_definitions_digest"`
	CustomDefinitions bool              `json:"channel_definitions"`
	SettingsSupported bool              `json:"channel_settings"`
	SettingsDigest    string            `json:"channel_settings_digest"`
	Instance          string            `json:"instance_id"`
	Revision          string            `json:"revision"`
	Atomic            bool              `json:"temporary_channel_restore"`
	Rules             []retainedRule    `json:"rules"`
	Channels          []retainedChannel `json:"temporary_channels"`
}
type bootstrapReceipt struct {
	SnapshotID      string `json:"snapshot_id"`
	AppliedRevision string `json:"applied_revision"`
	Unchanged       bool   `json:"unchanged"`
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
	if e == nil && v.Version != 1 && v.Version != 2 {
		e = errors.New("unsupported snapshot")
	}
	return v, e
}
func (s *Service) captureControls(ctx context.Context, src controlSource, live retainedLive, old retainedRecord) error {
	if !old.Enabled {
		return nil
	}
	if err := s.control.checkControlInstance(ctx, src, live.Instance); err != nil {
		return err
	}
	if old.Encrypted != "" && old.Target == controlTarget(src) && old.Instance != live.Instance {
		return errors.New("来源实例已变化，必须先核对恢复状态；已保存配置未覆盖")
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
	definitions := map[string]json.RawMessage{}
	snapshot := retainedSnapshot{Version: 1, Rules: append([]retainedRule{}, live.Rules...), Channels: []retainedChannel{}}
	if live.SettingsSupported && (live.SettingsDigest != tokenHash("{}") || live.CustomDefinitions) {
		admin := src
		if admin.ConfigKey != "" {
			admin.Key = admin.ConfigKey
		}
		exported, _, err := s.settingsGateway(ctx, admin, "GET", "/v1/channel-settings/export", nil)
		if err != nil {
			return err
		}
		if exported["revision"] != live.Revision {
			return errors.New("设置读取期间版本变化，请刷新后核对")
		}
		if err = decodeMap(exported["temporary_definitions"], &definitions); err != nil {
			return err
		}
		if err = decodeMap(exported["channel_settings"], &snapshot.Settings); err != nil {
			return err
		}
		if len(snapshot.Settings) > 0 {
			snapshot.Version = 2
		}
	} else if !live.SettingsSupported && old.Encrypted != "" {
		previous, err := s.control.retainedSnapshot(old)
		if err != nil {
			return err
		}
		if len(previous.Settings) > 0 {
			return errors.New("来源不支持已保存的高级设置，保留原版本")
		}
	}

	for _, p := range live.Channels {
		saved, ok := known[p.Provider]
		if raw, exists := definitions[p.Provider]; exists {
			var doc struct {
				Base string `json:"base_url"`
				API  any    `json:"api"`
			}
			if json.Unmarshal(raw, &doc) != nil {
				return errors.New("渠道定义无效")
			}
			keys := providerKeys(doc.API)
			// The complete definition can authenticate with cloud credentials
			// instead of api. This compatibility field only constructs a
			// prototype; the gateway then compiles the original definition.
			if len(keys) == 0 {
				keys = []string{"__full_definition__"}
			}
			saved = retainedChannel{Provider: p.Provider, KeyID: p.KeyID, Base: doc.Base, Key: keys[0], Definition: raw}
			ok = true
			snapshot.Version = 2
		}
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
	if len(saved.Settings) > 0 {
		if live.SettingsDigest != tokenHash(canonicalSettings(saved.Settings)) {
			return false
		}
	} else if live.SettingsDigest != "" && live.SettingsDigest != tokenHash("{}") {
		return false
	}
	definitions := map[string]json.RawMessage{}
	for _, c := range saved.Channels {
		if len(c.Definition) > 0 {
			definitions[c.Provider] = c.Definition
		}
	}
	if len(definitions) > 0 && live.DefinitionsDigest != tokenHash(canonicalSettings(definitions)) {
		return false
	}
	saved.Settings = nil
	saved.Version = 1

	scrub := func(v retainedSnapshot) string {
		for i := range v.Channels {
			v.Channels[i].Key = ""
			v.Channels[i].Base = ""
			v.Channels[i].Definition = nil
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
	if e = s.control.checkControlInstance(ctx, src, live.Instance); e != nil {
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
		e = s.control.adoptControlInstance(ctx, src, record, live)
		return raw, e
	}
	continuing := record.Restoring == live.Instance && record.RestoreRevision == live.Revision
	if !continuing && (live.Bootstrap != nil || len(live.Rules) > 0 || len(live.Channels) > 0 || (live.SettingsDigest != "" && live.SettingsDigest != tokenHash("{}"))) {
		if e = s.control.verifyBootstrap(ctx, src, live); e != nil {
			return nil, e
		}
	}
	_, e = s.control.db.ExecContext(ctx, `UPDATE console_control_snapshots SET restoring_instance=$2,restore_revision=$3,status='restoring',message='',checked_at=now() WHERE source_id=$1`, src.ID, live.Instance, live.Revision)
	if e != nil {
		return nil, e
	}
	apply := func(path string, body map[string]any) error {
		body["revision"] = live.Revision
		restoreSource := src
		if saved.Version == 2 && restoreSource.ConfigKey != "" {
			restoreSource.Key = restoreSource.ConfigKey
		}
		next, _, err := subGateway(ctx, restoreSource, "POST", path, body)
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
	e = s.control.adoptControlInstance(ctx, src, record, live)
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
	var status, message, instance, revision, encrypted string
	var rules, channels int
	var saved, restored, checked sql.NullInt64
	e = s.control.db.QueryRowContext(r.Context(), `SELECT enabled,status,message,rule_count,channel_count,extract(epoch FROM saved_at)::bigint,extract(epoch FROM restored_at)::bigint,extract(epoch FROM checked_at)::bigint,instance_id,revision,encrypted_snapshot FROM console_control_snapshots WHERE source_id=$1`, src.ID).Scan(&enabled, &status, &message, &rules, &channels, &saved, &restored, &checked, &instance, &revision, &encrypted)
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
	snapshotID := ""
	if encrypted != "" {
		snapshotID = tokenHash(encrypted)
	}
	writeJSON(w, 200, map[string]any{"enabled": enabled, "status": status, "message": message, "rules": rules, "channels": channels, "saved_at": stamp(saved), "restored_at": stamp(restored), "checked_at": stamp(checked), "saved_instance_id": instance, "saved_revision": revision, "snapshot_id": snapshotID})
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
	if saved.Version > 1 && r.Header.Get("X-Uni-Channel-Settings-Version") != "1" {
		http.Error(w, "saved channel settings require a compatible gateway", 409)
		return
	}
	// Keep the exact encrypted version issued at boot. A later saved edit must
	// not erase the evidence needed to recognize an unchanged older bootstrap.
	id := tokenHash(record.Encrypted)
	_, e = s.control.db.ExecContext(r.Context(), `INSERT INTO console_control_bootstraps(source_id,target_hash,snapshot_id,encrypted_snapshot,instance_id,revision) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, src.ID, record.Target, id, record.Encrypted, record.Instance, record.Revision)
	if e != nil {
		http.Error(w, "configuration receipt unavailable", 503)
		return
	}
	writeJSON(w, 200, map[string]any{"enabled": true, "snapshot_id": id, "snapshot": saved})
}
