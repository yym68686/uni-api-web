package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

//go:embed fingerprint/manifest.json
var fingerprintManifestJSON []byte

const fingerprintSampleBytes = 256 << 10

var fingerprintReferenceKey = func() string {
	h := sha256.New()
	h.Write(fingerprintModelsJSON)
	h.Write(fingerprintManifestJSON)
	return hex.EncodeToString(h.Sum(nil))
}()

type fingerprintSampleState struct {
	Version      int    `json:"version"`
	Model        string `json:"model"`
	Format       string `json:"format"`
	ReferenceKey string `json:"reference_key"`
	ChannelKey   string `json:"channel_key"`
	Entries      []struct {
		Numbers       []int `json:"numbers"`
		ExpectedCount int   `json:"expected_count"`
		Sample        struct {
			RequestID  string `json:"request_id"`
			State      string `json:"state"`
			DurationMS int64  `json:"duration_ms"`
		} `json:"sample"`
	} `json:"entries"`
}

func validateFingerprintSamples(raw json.RawMessage, config map[string]any) (int, bool) {
	var state fingerprintSampleState
	format, _ := config["format"].(string)
	if format == "" {
		format = "responses"
	}
	if len(raw) == 0 || len(raw) > fingerprintSampleBytes || json.Unmarshal(raw, &state) != nil || state.Version != 1 || state.Model != config["model"] || state.Format != format || state.ReferenceKey != fingerprintReferenceKey || len(state.Entries) < 1 || len(state.Entries) > 3 {
		return 0, false
	}
	ids, lengths := map[string]bool{}, map[int]bool{}
	for _, entry := range state.Entries {
		id, n := entry.Sample.RequestID, entry.ExpectedCount
		if n < 292 || n > 332 || lengths[n] || len(entry.Numbers) < max(80, (n*55+99)/100) || len(entry.Numbers) > 8192 || entry.Sample.State != "done" || entry.Sample.DurationMS < 0 || entry.Sample.DurationMS > 1<<53-1 || !strings.HasPrefix(id, "fingerprint-") || len(id) > len("fingerprint-")+64 || len(id) <= len("fingerprint-") || ids[id] {
			return 0, false
		}
		for _, ch := range id[len("fingerprint-"):] {
			if ch != '-' && (ch < 'a' || ch > 'z') && (ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9') {
				return 0, false
			}
		}
		for _, value := range entry.Numbers {
			if value < 1 || value > 355 {
				return 0, false
			}
		}
		ids[id], lengths[n] = true, true
	}
	return len(state.Entries), true
}

// The private loopback response is split before it can enter public result JSON.
// Only the separately named database column retains bounded numeric evidence.
func decodeFingerprintCheckpoint(raw []byte, config map[string]any) (json.RawMessage, json.RawMessage, error) {
	var result map[string]json.RawMessage
	if len(raw) > fingerprintSampleBytes || json.Unmarshal(raw, &result) != nil || result == nil {
		return nil, nil, errors.New("指纹采样结果无效")
	}
	samples := result["_resume"]
	delete(result, "_resume")
	var model, status string
	var used int
	if json.Unmarshal(result["model"], &model) != nil || model != config["model"] || json.Unmarshal(result["status"], &status) != nil || (status != "match" && status != "mismatch" && status != "error" && status != "insufficient") || json.Unmarshal(result["used_outputs"], &used) != nil || used < 0 || used > 3 {
		return nil, nil, errors.New("指纹采样结果无效")
	}
	if len(samples) > 0 {
		if count, valid := validateFingerprintSamples(samples, config); !valid || count != used {
			return nil, nil, errors.New("指纹续采数据无效")
		}
	} else if used > 0 {
		return nil, nil, errors.New("指纹续采数据缺失")
	}
	public, err := json.Marshal(result)
	return public, samples, err
}

func (s *Service) startFingerprintSamples(ctx context.Context, t fingerprintTarget, run string, plan fingerprintPlan) (json.RawMessage, error) {
	tx, err := s.control.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var raw []byte
	var revision string
	if err = tx.QueryRowContext(ctx, `SELECT sample_state,revision FROM console_model_fingerprints
 WHERE scope=$1 AND model=$2 AND run_id=$3 AND state='running' AND deadline>now() FOR UPDATE`, t.scope(), t.Model, run).Scan(&raw, &revision); err != nil {
		return nil, err
	}
	count, valid := validateFingerprintSamples(raw, plan.config)
	var saved fingerprintSampleState
	_ = json.Unmarshal(raw, &saved)
	sameChannel := saved.ChannelKey != "" && saved.ChannelKey == plan.sampleKey
	// Adopt an already checkpointed first-release round only when its original
	// ciphertext revision still matches. Never infer identity for legacy data.
	if saved.ChannelKey == "" {
		sameChannel = revision == plan.revision
	}
	if !sameChannel || !valid || count >= 3 {
		raw = nil
		_, err = tx.ExecContext(ctx, `UPDATE console_model_fingerprints SET result=NULL,sample_state=NULL,revision=$4 WHERE scope=$1 AND model=$2 AND run_id=$3`, t.scope(), t.Model, run, plan.revision)
	} else if revision != plan.revision {
		_, err = tx.ExecContext(ctx, `UPDATE console_model_fingerprints SET revision=$4 WHERE scope=$1 AND model=$2 AND run_id=$3`, t.scope(), t.Model, run, plan.revision)
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return json.RawMessage(raw), nil
}

func (s *Service) saveFingerprintSamples(ctx context.Context, t fingerprintTarget, run, revision, channelKey string, public, samples json.RawMessage) error {
	var retained any
	if len(samples) > 0 {
		var state map[string]json.RawMessage
		if err := json.Unmarshal(samples, &state); err != nil || state == nil || channelKey == "" {
			return errors.New("指纹渠道证据无效")
		}
		state["channel_key"], _ = json.Marshal(channelKey)
		bound, err := json.Marshal(state)
		if err != nil {
			return err
		}
		retained = string(bound)
	}
	result, err := s.control.db.ExecContext(ctx, `UPDATE console_model_fingerprints SET result=$5,sample_state=$6,updated_at=now()
 WHERE scope=$1 AND model=$2 AND run_id=$3 AND revision=$4 AND state='running' AND deadline>now()
 AND EXISTS(SELECT 1 FROM console_fingerprint_site_leases WHERE run_id=$3 AND deadline>now())`, t.scope(), t.Model, run, revision, string(public), retained)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return sql.ErrNoRows
	}
	return nil
}
