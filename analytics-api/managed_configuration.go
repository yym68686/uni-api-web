package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
)

const managedConfigLimit = 16 << 20

var errManagedConfigConflict = errors.New("managed configuration version changed")
var errManagedConfigInvalid = errors.New("invalid managed configuration")

type managedBundle struct {
	Schema   int             `json:"schema"`
	Base     json.RawMessage `json:"base"`
	Controls json.RawMessage `json:"controls"`
}

type managedVersion struct {
	Revision int64           `json:"revision"`
	Bundle   json.RawMessage `json:"bundle"`
}

type managedState struct {
	Published int64  `json:"published_revision"`
	Draft     int64  `json:"draft_revision"`
	Requested bool   `json:"requested"`
	Failure   string `json:"failure,omitempty"`
	Applied   int64  `json:"applied_revision,omitempty"`
}

type managedAction struct {
	Action    string          `json:"action"`
	Expected  int64           `json:"expected_revision"`
	Draft     int64           `json:"draft_revision"`
	Bundle    json.RawMessage `json:"bundle"`
	Operation string          `json:"operation_id"`
	Digest    string          `json:"operation_hash"`
	Credits   *float64        `json:"credits"`
}

type managedCommit struct {
	Revision  int64    `json:"revision"`
	Duplicate bool     `json:"duplicate,omitempty"`
	Credits   *float64 `json:"credits,omitempty"`
}

func managedRuntimeCredential(src controlSource) string {
	if src.configKeyError != nil {
		return ""
	}
	if src.ConfigKey != "" {
		return src.ConfigKey
	}
	return src.Key
}

func managedAuthorized(header, key string) bool {
	raw, ok := strings.CutPrefix(header, "Bearer ")
	if !ok || len(raw) < 8 || len(raw) > 4096 || key == "" {
		return false
	}
	a, b := sha256.Sum256([]byte(raw)), sha256.Sum256([]byte(key))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

func decodeManagedAction(w http.ResponseWriter, r *http.Request) (managedAction, bool) {
	var action managedAction
	limit := int64(managedConfigLimit + 2048)
	if configured := configurationByteLimit(); configured > 0 && configured < limit {
		limit = configured
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	d.DisallowUnknownFields()
	if err := d.Decode(&action); err != nil {
		var limitErr *http.MaxBytesError
		if errors.As(err, &limitErr) {
			http.Error(w, "configuration payload too large", 413)
		} else {
			http.Error(w, "invalid configuration request", 400)
		}
		return action, false
	}
	if d.Decode(new(any)) != io.EOF || action.Expected < 0 || action.Draft < 0 {
		http.Error(w, "invalid configuration request", 400)
		return action, false
	}
	return action, true
}

func checkManagedBundle(raw json.RawMessage) error {
	if len(raw) > managedConfigLimit {
		return errManagedConfigInvalid
	}
	var bundle managedBundle
	if json.Unmarshal(raw, &bundle) != nil || bundle.Schema != 1 ||
		len(bundle.Base) == 0 || bundle.Base[0] != '{' ||
		len(bundle.Controls) == 0 || bundle.Controls[0] != '{' {
		return errManagedConfigInvalid
	}
	return nil
}

func (s *controlStore) managedState(ctx context.Context, q migrationReader, id string, lock bool) (state managedState, err error) {
	query := `SELECT coalesce(published_revision,0),coalesce(draft_revision,0),requested,failure
		FROM console_managed_config_state WHERE source_id=$1`
	if lock {
		query += " FOR UPDATE"
	}
	err = q.QueryRowContext(ctx, query, id).Scan(&state.Published, &state.Draft, &state.Requested, &state.Failure)
	if errors.Is(err, sql.ErrNoRows) && !lock {
		err = nil
	}
	return
}

func (s *controlStore) managedVersion(ctx context.Context, tx *sql.Tx, id string, rev int64) (*managedVersion, error) {
	var encrypted string
	if err := tx.QueryRowContext(ctx, `SELECT encrypted_bundle FROM console_managed_config_versions WHERE source_id=$1 AND revision=$2`, id, rev).Scan(&encrypted); err != nil {
		return nil, err
	}
	plain, err := s.decrypt(encrypted)
	bundle, bound := strings.CutPrefix(plain, id+"\n")
	if err != nil || !bound || !json.Valid([]byte(bundle)) {
		return nil, errManagedConfigInvalid
	}
	return &managedVersion{Revision: rev, Bundle: json.RawMessage(bundle)}, nil
}

func (s *controlStore) managedVersions(ctx context.Context, id string, applied int64) (any, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	state, err := s.managedState(ctx, tx, id, false)
	if err != nil {
		return nil, err
	}
	out := struct {
		Revision      int64           `json:"revision"`
		Uninitialized bool            `json:"uninitialized"`
		Published     *managedVersion `json:"published,omitempty"`
		Pending       *managedVersion `json:"pending,omitempty"`
	}{Revision: state.Published, Uninitialized: state.Published == 0 && state.Draft == 0}
	if state.Published != 0 && applied != state.Published {
		out.Published, err = s.managedVersion(ctx, tx, id, state.Published)
		if err != nil {
			return nil, err
		}
	}
	if state.Requested && state.Draft > state.Published {
		out.Pending, err = s.managedVersion(ctx, tx, id, state.Draft)
		if err != nil {
			return nil, err
		}
	}
	return out, tx.Commit()
}

func (s *controlStore) saveManagedConfig(ctx context.Context, id string, in managedAction) (out managedCommit, err error) {
	if in.Operation != "" {
		if in.Action != "commit" || len(in.Operation) > 128 || len(in.Digest) != 64 ||
			in.Credits == nil || math.IsInf(*in.Credits, 0) || math.IsNaN(*in.Credits) {
			return out, errManagedConfigInvalid
		}
	} else if in.Digest != "" || in.Credits != nil {
		return out, errManagedConfigInvalid
	}
	var encrypted string
	if in.Action == "draft" || in.Action == "commit" {
		if err = checkManagedBundle(in.Bundle); err != nil {
			return out, err
		}
		encrypted, err = s.encrypt(id + "\n" + string(in.Bundle))
		if err != nil {
			return out, err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO console_managed_config_state(source_id) VALUES($1) ON CONFLICT DO NOTHING`, id)
	if err != nil {
		return out, err
	}
	current, err := s.managedState(ctx, tx, id, true)
	if err != nil {
		return out, err
	}
	if in.Operation != "" {
		var digest string
		var credits float64
		err = tx.QueryRowContext(ctx, `SELECT operation_hash,revision,credits FROM console_managed_config_operations
			WHERE source_id=$1 AND operation_id=$2`, id, in.Operation).Scan(&digest, &out.Revision, &credits)
		if err == nil {
			if digest != in.Digest {
				return out, errManagedConfigConflict
			}
			out.Duplicate, out.Credits = true, &credits
			return out, tx.Commit()
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
	}
	if current.Published != in.Expected {
		return out, errManagedConfigConflict
	}
	insert := func() error {
		return tx.QueryRowContext(ctx, `INSERT INTO console_managed_config_versions(source_id,encrypted_bundle)
			VALUES($1,$2) RETURNING revision`, id, encrypted).Scan(&out.Revision)
	}
	switch in.Action {
	case "draft":
		if current.Draft != in.Draft || current.Requested {
			return out, errManagedConfigConflict
		}
		if err = insert(); err != nil {
			return out, err
		}
		_, err = tx.ExecContext(ctx, `UPDATE console_managed_config_state SET draft_revision=$2,requested=false,failure='' WHERE source_id=$1`, id, out.Revision)
	case "publish", "promote", "reject":
		if current.Draft <= current.Published || in.Draft != current.Draft ||
			(in.Action == "publish") == current.Requested {
			return out, errManagedConfigConflict
		}
		out.Revision = current.Published
		switch in.Action {
		case "publish":
			_, err = tx.ExecContext(ctx, `UPDATE console_managed_config_state SET requested=true,failure='' WHERE source_id=$1`, id)
		case "promote":
			out.Revision = current.Draft
			_, err = tx.ExecContext(ctx, `UPDATE console_managed_config_state SET published_revision=draft_revision,draft_revision=NULL,requested=false,failure='' WHERE source_id=$1`, id)
		case "reject":
			_, err = tx.ExecContext(ctx, `UPDATE console_managed_config_state SET requested=false,failure='配置校验未通过，原版本未变更' WHERE source_id=$1`, id)
		}
	case "commit":
		if current.Published == 0 || current.Requested {
			return out, errManagedConfigConflict
		}
		if err = insert(); err != nil {
			return out, err
		}
		// Preserve the operator's rejected/stale draft for review, but never let
		// a draft staged against the old base publish over an intervening edit.
		_, err = tx.ExecContext(ctx, `UPDATE console_managed_config_state SET published_revision=$2,
			failure=CASE WHEN draft_revision IS NULL THEN '' ELSE '生效配置已变化，请重新创建草稿' END WHERE source_id=$1`, id, out.Revision)
		if err == nil && in.Operation != "" {
			_, err = tx.ExecContext(ctx, `INSERT INTO console_managed_config_operations(source_id,operation_id,operation_hash,revision,credits)
				VALUES($1,$2,$3,$4,$5)`, id, in.Operation, in.Digest, out.Revision, in.Credits)
			out.Credits = in.Credits
		}
	default:
		return out, errManagedConfigInvalid
	}
	if err != nil {
		return out, err
	}
	// Bound storage without deleting the current or draft configuration.
	if in.Action == "commit" || in.Action == "draft" {
		_, err = tx.ExecContext(ctx, `DELETE FROM console_managed_config_versions v WHERE v.source_id=$1
			AND v.revision NOT IN (SELECT revision FROM console_managed_config_versions WHERE source_id=$1 ORDER BY revision DESC LIMIT 20)
			AND NOT EXISTS (SELECT 1 FROM console_managed_config_state s WHERE s.source_id=v.source_id
				AND v.revision IN (s.published_revision,s.draft_revision))`, id)
		if err != nil {
			return out, err
		}
	}
	return out, tx.Commit()
}

func managedConfigError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errManagedConfigConflict):
		http.Error(w, "配置版本已变化，请刷新后核对", 409)
	case errors.Is(err, errManagedConfigInvalid):
		http.Error(w, "配置格式无效", 400)
	default:
		http.Error(w, "配置存储暂不可用", 503)
	}
}

func (s *Service) runtimeConfiguration(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.control == nil {
		http.Error(w, "unavailable", 503)
		return
	}
	src, err := s.control.source(r.Context(), r.PathValue("id"))
	if err != nil || !managedAuthorized(r.Header.Get("Authorization"), managedRuntimeCredential(src)) {
		http.Error(w, "unauthorized", 401)
		return
	}
	if r.Method == http.MethodGet {
		applied, _ := strconv.ParseInt(r.URL.Query().Get("applied_revision"), 10, 64)
		result, err := s.control.managedVersions(r.Context(), src.ID, applied)
		if err != nil {
			managedConfigError(w, err)
			return
		}
		writeJSON(w, 200, result)
		return
	}
	in, ok := decodeManagedAction(w, r)
	if !ok {
		return
	}
	if in.Action != "promote" && in.Action != "reject" && in.Action != "commit" {
		managedConfigError(w, errManagedConfigInvalid)
		return
	}
	out, err := s.control.saveManagedConfig(r.Context(), src.ID, in)
	if err != nil {
		managedConfigError(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func (s *Service) manageConfiguration(w http.ResponseWriter, r *http.Request) {
	src, err := s.control.source(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, "来源不存在", 404)
		return
	}
	if r.Method == http.MethodGet {
		result, err := s.control.managedState(r.Context(), s.control.db, src.ID, false)
		if err != nil {
			managedConfigError(w, err)
			return
		}
		if result.Published > 0 {
			raw, _, err := subGateway(r.Context(), src, "GET", "/v1/channel-controls", nil)
			if err == nil {
				if revision, ok := raw["managed_config_revision"].(float64); ok {
					result.Applied = int64(revision)
				}
			}
		}
		writeJSON(w, 200, result)
		return
	}
	in, ok := decodeManagedAction(w, r)
	if !ok {
		return
	}
	switch in.Action {
	case "import":
		src.Key = managedRuntimeCredential(src)
		if src.Key == "" {
			http.Error(w, "请先设置来源管理员密钥", 403)
			return
		}
		unlock, lockErr := s.control.lockControls(r.Context(), src.ID)
		if lockErr != nil {
			http.Error(w, "来源配置正在变更", 409)
			return
		}
		defer unlock()
		raw, code, err := subGateway(r.Context(), src, "GET", "/v1/configuration/export", nil)
		if err != nil {
			http.Error(w, "无法导入；请确认在线网关版本和管理员密钥", code)
			return
		}
		in.Bundle, err = json.Marshal(raw["bundle"])
		if err != nil {
			managedConfigError(w, errManagedConfigInvalid)
			return
		}
		in.Action = "draft"
	case "draft", "publish":
	default:
		managedConfigError(w, errManagedConfigInvalid)
		return
	}
	if _, err = s.control.saveManagedConfig(r.Context(), src.ID, in); err != nil {
		managedConfigError(w, err)
		return
	}
	result, err := s.control.managedState(r.Context(), s.control.db, src.ID, false)
	if err != nil {
		managedConfigError(w, err)
		return
	}
	writeJSON(w, 200, result)
}
