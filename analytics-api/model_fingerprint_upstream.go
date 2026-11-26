package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const fingerprintUpstreamHeadURL = "https://api.github.com/repos/Ikaleio/lm-detector/commits/main"

func upstreamFingerprintHead(ctx context.Context, client *http.Client) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fingerprintUpstreamHeadURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github.sha")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("upstream head HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 128))
	commit := strings.TrimSpace(string(raw))
	if err != nil || !fingerprintCommit.MatchString(commit) {
		return "", errors.New("invalid upstream commit")
	}
	return commit, nil
}

// Only the local, versioned builder can create data for this path. It reads
// immutable upstream files, checks the pinned sampler/scorer and validates
// every model offline. In particular this is not an upload or signing API.
func deriveUpstreamFingerprintBundle(ctx context.Context, commit string, sequence int64) ([]byte, error) {
	dir, err := os.MkdirTemp("", "uni-fingerprint-upstream-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	output := filepath.Join(dir, "catalog.json")
	priorPath := filepath.Join(dir, "prior.json")
	current := currentFingerprintCatalog()
	prior, err := json.Marshal(struct {
		Models    map[string]fingerprintReference `json:"models"`
		Protocols map[string]string               `json:"protocols"`
	}{current.models, current.protocols})
	if err != nil {
		return nil, err
	}
	if err = os.WriteFile(priorPath, prior, 0o600); err != nil {
		return nil, err
	}
	scripts := os.Getenv("MODEL_FINGERPRINT_DIR")
	if scripts == "" {
		scripts = "fingerprint"
	}
	cmd := exec.CommandContext(ctx, "node", "--max-old-space-size=192", "--max-semi-space-size=4",
		filepath.Join(scripts, "publish-catalog.mjs"), "--derive", commit, output, priorPath)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "NODE_NO_WARNINGS=1"}
	if err = cmd.Run(); err != nil {
		return nil, fmt.Errorf("offline upstream validation failed at %s: %w", commit, err)
	}
	file, err := os.Open(output)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, fingerprintCatalogLimit+1))
	if err != nil {
		return nil, err
	}
	var bundle fingerprintBundle
	if len(raw) > fingerprintCatalogLimit || json.Unmarshal(raw, &bundle) != nil || bundle.Sequence != 0 {
		return nil, errors.New("invalid local fingerprint candidate")
	}
	bundle.Sequence = sequence
	return json.Marshal(bundle)
}

func compatibleFingerprintCatalog(old, candidate *fingerprintCatalog) error {
	for model, ref := range old.models {
		if candidate.models[model] != ref || candidate.protocols[model] != old.protocols[model] {
			return fmt.Errorf("existing fingerprint request contract changed: %s", model)
		}
	}
	return nil
}

func fingerprintCatalogNeedsRefresh(catalog *fingerprintCatalog, upstreamCommit string) bool {
	return upstreamCommit != catalog.commit || catalog.builderVersion < fingerprintCatalogBuilderVersion
}

func (s *Service) loadUpstreamFingerprintCatalog(ctx context.Context) error {
	var raw []byte
	err := s.control.db.QueryRowContext(ctx, `SELECT bundle FROM console_fingerprint_upstream_catalog WHERE id AND sequence > $1`, currentFingerprintCatalog().sequence).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	bundle, candidate, err := parseFingerprintBundle(raw)
	if err != nil {
		return err
	}
	if err = compatibleFingerprintCatalog(currentFingerprintCatalog(), candidate); err != nil {
		return err
	}
	return activateFingerprintCatalog(ctx, bundle, candidate, nil)
}

func (s *Service) refreshUpstreamFingerprintCatalog(ctx context.Context, client *http.Client, derive func(context.Context, string, int64) ([]byte, error)) error {
	// No table locks are held across network or offline validation work. This
	// transaction-scoped advisory lock deduplicates concurrent replica polls.
	tx, err := s.control.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var acquired bool
	// Use the two-key namespace; one-key locks guard unrelated analytics DML.
	err = tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock(8395012, 19)`).Scan(&acquired)
	if err != nil || !acquired {
		return err
	}
	if err = s.loadUpstreamFingerprintCatalog(ctx); err != nil {
		return err
	}
	commit, err := upstreamFingerprintHead(ctx, client)
	if err != nil {
		return err
	}
	old := currentFingerprintCatalog()
	if !fingerprintCatalogNeedsRefresh(old, commit) {
		log.Printf("fingerprint_upstream_current commit=%s builder=%d models=%d", commit, old.builderVersion, len(old.names))
		return nil
	}
	build, cancel := context.WithTimeout(ctx, 2*time.Minute)
	raw, err := derive(build, commit, max(old.sequence+1, time.Now().Unix()))
	cancel()
	if err != nil {
		return err
	}
	bundle, candidate, err := parseFingerprintBundle(raw)
	if err != nil {
		return err
	}
	if candidate.commit != commit || candidate.builderVersion != fingerprintCatalogBuilderVersion {
		return errors.New("fingerprint candidate commit or builder version mismatch")
	}
	if err = compatibleFingerprintCatalog(old, candidate); err != nil {
		return err
	}
	return activateFingerprintCatalog(ctx, bundle, candidate, func(ctx context.Context) error {
		result, err := tx.ExecContext(ctx, `INSERT INTO console_fingerprint_upstream_catalog(id,sequence,bundle)
 VALUES(true,$1,$2) ON CONFLICT(id) DO UPDATE SET sequence=excluded.sequence,bundle=excluded.bundle
 WHERE console_fingerprint_upstream_catalog.sequence < excluded.sequence`, candidate.sequence, raw)
		if err = fingerprintSaved(result, err); err != nil {
			return err
		}
		return tx.Commit()
	})
}

func (s *Service) pollUpstreamFingerprintCatalog(ctx context.Context) {
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for ctx.Err() == nil {
		// The builder has its own short timeout; activation can wait for
		// an existing round (up to 13 minutes) without interrupting it.
		poll, cancel := context.WithTimeout(ctx, 17*time.Minute)
		if err := s.refreshUpstreamFingerprintCatalog(poll, client, deriveUpstreamFingerprintBundle); err != nil && ctx.Err() == nil {
			log.Printf("fingerprint_upstream_update_deferred: %v", err)
		}
		cancel()
		if !waitStartup(ctx, 15*time.Minute) {
			return
		}
	}
}
