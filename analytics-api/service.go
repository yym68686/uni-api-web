package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Service struct {
	newAPILogGap       *time.Duration // nil uses conservative production pacing
	auth               *authorizer
	engine             *Engine
	cfg                Config
	active             atomic.Int64
	lastCollect        atomic.Int64
	importFailures     atomic.Uint64
	loginMu            sync.Mutex
	loginWindow        time.Time
	loginAttempts      int
	cacheMu            sync.Mutex
	cache              map[string]cachedAnalytics
	historyLoading     atomic.Bool
	startup            atomic.Pointer[analyticsStartup]
	control            *controlStore
	keyDirectories     keyDirectoryCache
	summaries          summaryHistory
	observationHealth  sourceObservationCircuit
	managementComputed managementComputedPages
	modelDirectories   managementModelDirectories
}

type cachedAnalytics struct {
	expires  time.Time
	revision uint64
	body     []byte
}

func NewService(e *Engine, cfg Config) (*Service, error) {
	if e == nil {
		return nil, errors.New("engine required")
	}
	if cfg.PublicOrigin != "" {
		u, err := url.Parse(cfg.PublicOrigin)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return nil, errors.New("PUBLIC_ORIGIN must be an HTTP(S) origin")
		}
	}
	upstream := cfg.Upstream
	if upstream == "" && cfg.ControlMasterKey != "" {
		upstream = "http://localhost"
	}
	auth, err := newAuthorizer(upstream)
	if err != nil {
		return nil, err
	}
	return &Service{engine: e, cfg: cfg, auth: auth, cache: make(map[string]cachedAnalytics)}, nil
}
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /readyz", s.ready)
	mux.HandleFunc("GET /v1/runtime-restore/{id}", s.bootstrapControls)
	mux.HandleFunc("POST /v1/runtime-facts/{id}", s.ingestFacts)
	control := s.controlHandler()
	mux.Handle("/v1/auth/", control)
	mux.Handle("/v1/sources", control)
	mux.Handle("/v1/sources/", control)
	mux.Handle("/v1/sub2api/", control)
	mux.Handle("/v1/automations", control)
	mux.Handle("/v1/automations/", control)
	mux.Handle("/v1/channel-sites", control)
	mux.Handle("/v1/model-fingerprints", control)
	mux.Handle("/v1/channel-management", control)
	mux.Handle("/v1/channel-management/", control)
	mux.Handle("/v1/channel-spend", control)
	mux.Handle("/v1/channel-setting-templates", control)

	mux.HandleFunc("GET /v1/analytics", s.analytics)
	mux.HandleFunc("GET /v1/key-request-stats", s.keyRequestStats)
	mux.HandleFunc("GET /v1/channel-model-stats", s.channelModelStats)
	mux.HandleFunc("GET /v1/request-trace", s.requestTrace)
	mux.HandleFunc("GET /v1/request-logs", s.requestLogs)
	mux.HandleFunc("GET /v1/prices", s.prices)
	mux.HandleFunc("PUT /v1/prices/{model}", s.savePrice)
	mux.HandleFunc("GET /v1/status", s.status)
	return observeConsoleRequests(s.authenticate(mux))
}
func (s *Service) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"status": "ok", "revision": s.engine.Revision.Load(), "last_collect_ms": s.lastCollect.Load(), "state_ready": true, "analytics_ready": s.analyticsReady()})
}
func (s *Service) ready(w http.ResponseWriter, r *http.Request) {
	if !s.analyticsReady() {
		s.initializing(w)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	if err := s.engine.DB.PingContext(ctx); err != nil {
		writeJSON(w, 503, map[string]string{"code": "database_unavailable"})
		return
	}
	s.health(w, r)
}
func (s *Service) analyticsReady() bool { return !s.historyLoading.Load() }
func (s *Service) initializing(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "5")
	writeJSON(w, http.StatusServiceUnavailable, map[string]any{"code": "analytics_initializing", "error": "historical analytics initializing; retry shortly"})
}
func (s *Service) status(w http.ResponseWriter, r *http.Request) {
	var size, limit, high, floor, evicted int64
	if err := s.engine.DB.QueryRowContext(r.Context(), "SELECT pg_database_size(current_database()),database_limit_bytes,ingest_limit_bytes,evicted_before_ms,evicted_events FROM storage_state WHERE singleton").Scan(&size, &limit, &high, &floor, &evicted); err != nil {
		http.Error(w, "storage status unavailable", 503)
		return
	}
	writeJSON(w, 200, map[string]any{"status": "ok", "backend": "postgresql", "revision": s.engine.Revision.Load(), "active_ingest": s.active.Load(), "last_collect_ms": s.lastCollect.Load(), "import_failures": s.importFailures.Load(), "state_ready": true, "analytics_ready": s.analyticsReady(), "startup": s.startup.Load(), "storage": map[string]any{"database_bytes": size, "limit_bytes": limit, "ingest_limit_bytes": high, "evicted_before_ms": floor, "evicted_events": evicted}})
}

func (s *Service) analytics(w http.ResponseWriter, r *http.Request) {
	if !s.analyticsReady() {
		s.initializing(w)
		return
	}
	q := r.URL.Query()
	name := q.Get("range")
	if name == "" {
		name = "15m"
	}
	allowed, sourceErr := s.analyticsSources(r)
	if sourceErr != nil {
		http.Error(w, "source unavailable", 404)
		return
	}
	if q.Get("include_spend") == "true" {
		s.analyticsWithSpend(w, r, allowed, name)
		return
	}
	key := q.Encode() + "|" + strings.Join(allowed, ",")
	rev, revisionErr := s.engine.currentRevision(r.Context())
	if revisionErr != nil {
		http.Error(w, "analytics unavailable", 503)
		return
	}
	now := time.Now()
	s.cacheMu.Lock()
	if item, ok := s.cache[key]; ok && item.revision == rev && now.Before(item.expires) {
		body := append([]byte(nil), item.body...)
		s.cacheMu.Unlock()
		s.writeAnalyticsBody(w, r, body)
		return
	}
	s.cacheMu.Unlock()
	result, err := s.engine.Query(r.Context(), QueryFilter{SourceIDs: allowed, SourceID: q.Get("source_id"), Range: name, Model: q.Get("model"), UpstreamModel: q.Get("upstream_model"), Provider: q.Get("provider"), Endpoint: q.Get("endpoint"), Stream: q.Get("stream"), KeyID: q.Get("key_id"), Timeseries: q.Get("timeseries") == "true"})
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": "analytics unavailable"})
		return
	}
	result.Import = s.importStatus(allowed)
	body, err := json.Marshal(result)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "analytics unavailable"})
		return
	}
	s.cacheMu.Lock()
	s.cache[key] = cachedAnalytics{expires: now.Add(2 * time.Second), revision: rev, body: append([]byte(nil), body...)}
	if len(s.cache) > 256 {
		for k := range s.cache {
			delete(s.cache, k)
			break
		}
	}
	s.cacheMu.Unlock()
	s.writeAnalyticsBody(w, r, body)
}
func (s *Service) writeAnalyticsBody(w http.ResponseWriter, r *http.Request, body []byte) {
	if len(body) > 1024 && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		var compressed bytes.Buffer
		gz := gzip.NewWriter(&compressed)
		_, _ = gz.Write(body)
		_ = gz.Close()
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")
		w.Header().Set("Content-Length", strconv.Itoa(compressed.Len()))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(compressed.Bytes())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	_, _ = w.Write(body)
}
func (s *Service) prices(w http.ResponseWriter, r *http.Request) {
	out, err := s.engine.Prices(r.Context())
	if err != nil {
		http.Error(w, "prices unavailable", 503)
		return
	}
	writeJSON(w, 200, map[string]any{"data": out})
}
func (s *Service) savePrice(w http.ResponseWriter, r *http.Request) {
	var p Price
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&p); err != nil {
		http.Error(w, "invalid price", 400)
		return
	}
	p.Model = r.PathValue("model")
	// Tier metadata is authoritative catalog policy, never a client override.
	p.PromptPriceTier = catalogPromptPriceTier(p.Model)
	if err := validatePrice(p); err != nil {
		http.Error(w, "invalid price", 400)
		return
	}
	err := s.engine.SavePrice(r.Context(), p)
	if err != nil {
		status := http.StatusServiceUnavailable
		message := "price storage unavailable; refresh before retrying"
		if errors.Is(err, errPriceConflict) {
			status = http.StatusConflict
			message = err.Error()
		}
		writeJSON(w, status, map[string]string{"error": message})
		return
	}
	writeJSON(w, 200, map[string]any{"price": p})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
