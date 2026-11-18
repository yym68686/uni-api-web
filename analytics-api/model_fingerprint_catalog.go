package main

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"database/sql"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// This branch contains signed data only. No downloaded script is ever run by
// either the updater or the analytics service.
const fingerprintCatalogURL = "https://raw.githubusercontent.com/yym68686/uni-api-web/fingerprint-data-v1/bundle.json"
const fingerprintCatalogLimit = 16 << 20

//go:embed fingerprint/publisher.pub
var fingerprintPublisherKey string

type fingerprintCatalog struct {
	models               map[string]fingerprintReference
	protocols            map[string]string
	names                []string
	commit, referenceKey string
	sequence             int64
	process              *fingerprintProcess
}

var (
	fingerprintCatalogGate      sync.RWMutex
	publishedFingerprintCatalog atomic.Pointer[fingerprintCatalog]
	fingerprintID               = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)
	fingerprintCommit           = regexp.MustCompile(`^[0-9a-f]{40}$`)
	fingerprintSeed             = func() *fingerprintCatalog {
		var manifest struct{ Commit string }
		if err := json.Unmarshal(fingerprintManifestJSON, &manifest); err != nil {
			panic(err)
		}
		return &fingerprintCatalog{models: fingerprintModels, protocols: fingerprintProtocols, names: seedFingerprintModelOrder(),
			commit: manifest.Commit, referenceKey: fingerprintReferenceKey, process: &sharedFingerprintWorker}
	}()
)

func currentFingerprintCatalog() *fingerprintCatalog {
	if catalog := publishedFingerprintCatalog.Load(); catalog != nil {
		return catalog
	}
	return fingerprintSeed
}

type fingerprintBundle struct {
	Schema   int               `json:"schema"`
	Sequence int64             `json:"sequence"`
	Files    map[string][]byte `json:"files"`
}

var fingerprintBundleFiles = []string{"models.json", "protocols.json", "manifest.json", "data/unified_bank.json.gz", "data/shared_detector.bin.gz"}

func verifyFingerprintBundle(raw []byte, key ed25519.PublicKey) (fingerprintBundle, *fingerprintCatalog, error) {
	var signed struct {
		Payload   json.RawMessage `json:"payload"`
		Signature []byte          `json:"signature"`
	}
	var bundle fingerprintBundle
	catalog := &fingerprintCatalog{}
	fail := func() (fingerprintBundle, *fingerprintCatalog, error) {
		return fingerprintBundle{}, nil, errors.New("invalid fingerprint bundle signature or schema")
	}
	if len(raw) > fingerprintCatalogLimit || len(key) != ed25519.PublicKeySize || json.Unmarshal(raw, &signed) != nil || !ed25519.Verify(key, signed.Payload, signed.Signature) ||
		json.Unmarshal(signed.Payload, &bundle) != nil || bundle.Schema != 1 || bundle.Sequence <= 0 || len(bundle.Files) != len(fingerprintBundleFiles) {
		return fail()
	}
	for _, name := range fingerprintBundleFiles {
		if len(bundle.Files[name]) == 0 {
			return fail()
		}
	}
	var manifest struct {
		Commit string
		Files  map[string]string
	}
	if json.Unmarshal(bundle.Files["manifest.json"], &manifest) != nil || !fingerprintCommit.MatchString(manifest.Commit) ||
		json.Unmarshal(bundle.Files["models.json"], &catalog.models) != nil || json.Unmarshal(bundle.Files["protocols.json"], &catalog.protocols) != nil ||
		len(catalog.models) < len(fingerprintModels) || len(catalog.models) > 512 || len(catalog.models) != len(catalog.protocols) {
		return fail()
	}
	for model, ref := range catalog.models {
		protocol := catalog.protocols[model]
		if !fingerprintID.MatchString(model) || !fingerprintID.MatchString(ref.ClassID) ||
			(protocol != "responses" && protocol != "chat" && protocol != "gemini") ||
			(ref.Effort != "low" && ref.Effort != "default") {
			return fail()
		}
		if old, exists := fingerprintModels[model]; exists && (ref != old || protocol != fingerprintProtocols[model]) {
			return fail()
		}
		catalog.names = append(catalog.names, model)
	}
	for seed := range fingerprintModels {
		if _, ok := catalog.models[seed]; !ok {
			return fail()
		}
	}
	sort.Strings(catalog.names)
	catalog.commit, catalog.sequence = manifest.Commit, bundle.Sequence
	catalog.referenceKey = makeFingerprintReferenceKey(bundle.Files["models.json"], bundle.Files["manifest.json"])
	return bundle, catalog, nil
}

func closeFingerprintCatalog(catalog *fingerprintCatalog) {
	catalog.process.close()
	if catalog.process.dataDir != "" {
		_ = os.RemoveAll(catalog.process.dataDir) // only our private MkdirTemp directory
	}
}

func (s *Service) activateFingerprintBundle(ctx context.Context, raw []byte, key ed25519.PublicKey, persist bool) error {
	bundle, candidate, err := verifyFingerprintBundle(raw, key)
	if err != nil {
		return err
	}
	if candidate.sequence <= currentFingerprintCatalog().sequence {
		return nil
	}
	dir, err := os.MkdirTemp("", "uni-fingerprint-data-")
	if err != nil {
		return err
	}
	candidate.process = &fingerprintProcess{dataDir: dir, referenceKey: candidate.referenceKey}
	adopted := false
	defer func() {
		if !adopted {
			closeFingerprintCatalog(candidate)
		}
	}()
	if err = os.Mkdir(filepath.Join(dir, "data"), 0700); err != nil {
		return err
	}
	for _, name := range fingerprintBundleFiles {
		if err = os.WriteFile(filepath.Join(dir, name), bundle.Files[name], 0600); err != nil {
			return err
		}
	}
	// Warm and validate before waiting for current rounds to drain. A failed
	// candidate never closes the old scorer or replaces the saved LKG.
	if _, err = candidate.process.get(ctx); err != nil {
		return err
	}
	fingerprintCatalogGate.Lock()
	defer fingerprintCatalogGate.Unlock()
	old := currentFingerprintCatalog()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if candidate.sequence <= old.sequence {
		return nil
	}
	if _, err = candidate.process.get(ctx); err != nil {
		return err
	}
	if persist {
		save, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		result, err := s.control.db.ExecContext(save, `INSERT INTO console_fingerprint_catalog(id,sequence,bundle)
		  VALUES(true,$1,$2) ON CONFLICT(id) DO UPDATE SET sequence=excluded.sequence,bundle=excluded.bundle
		  WHERE console_fingerprint_catalog.sequence < excluded.sequence`, candidate.sequence, raw)
		if err != nil {
			return err
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			return errors.New("catalog publication was superseded; will reload saved catalog")
		}
	}
	publishedFingerprintCatalog.Store(candidate)
	adopted = true
	closeFingerprintCatalog(old)
	log.Printf("fingerprint_catalog_activated commit=%s generation=%d models=%d", candidate.commit, candidate.sequence, len(candidate.names))
	return nil
}

func (s *Service) loadSavedFingerprintBundle(ctx context.Context, key ed25519.PublicKey) error {
	var raw []byte
	err := s.control.db.QueryRowContext(ctx, `SELECT bundle FROM console_fingerprint_catalog WHERE id AND sequence > $1`, currentFingerprintCatalog().sequence).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.activateFingerprintBundle(ctx, raw, key, false)
}

func (s *Service) pollFingerprintCatalog(ctx context.Context, key ed25519.PublicKey) {
	client := &http.Client{Timeout: 40 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	etag := ""
	for ctx.Err() == nil {
		if err := s.loadSavedFingerprintBundle(ctx, key); err != nil && ctx.Err() == nil {
			log.Printf("fingerprint_catalog_saved_rejected: %v", err)
		}
		next, err := s.fetchFingerprintBundle(ctx, key, client, etag)
		if err != nil && ctx.Err() == nil {
			log.Printf("fingerprint_catalog_update_deferred: %v", err)
		} else if next != "" {
			etag = next
		}
		if !waitStartup(ctx, 15*time.Minute) {
			return
		}
	}
}

func (s *Service) fetchFingerprintBundle(ctx context.Context, key ed25519.PublicKey, client *http.Client, etag string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", fingerprintCatalogURL, nil)
	if err != nil {
		return "", err
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		return etag, nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("bundle source HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, fingerprintCatalogLimit+1))
	if err != nil {
		return "", err
	}
	if err = s.activateFingerprintBundle(ctx, raw, key, true); err != nil {
		return "", err
	}
	return resp.Header.Get("ETag"), nil
}

func (s *Service) fingerprintRuntime(ctx context.Context) {
	der, err := base64.StdEncoding.DecodeString(strings.TrimSpace(fingerprintPublisherKey))
	if err != nil {
		panic("invalid fingerprint publisher key")
	}
	pub, err := x509.ParsePKIXPublicKey(der)
	key, ok := pub.(ed25519.PublicKey)
	if err != nil || !ok {
		panic("invalid fingerprint publisher key")
	}
	loading, cancel := context.WithTimeout(ctx, 35*time.Second)
	err = s.loadSavedFingerprintBundle(loading, key)
	cancel()
	if err != nil && ctx.Err() == nil {
		log.Printf("fingerprint_catalog_saved_rejected: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if os.Getenv("FINGERPRINT_CATALOG_SYNC") == "1" {
			s.pollFingerprintCatalog(ctx, key)
		}
	}()
	s.fingerprintLoop(ctx)
	<-done
	closeFingerprintCatalog(currentFingerprintCatalog())
	publishedFingerprintCatalog.Store(nil)
}
