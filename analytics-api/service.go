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

	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type Service struct {
	auth             *authorizer
	engine           *Engine
	cfg              Config
	active           atomic.Int64
	lastCollect      atomic.Int64
	remaining        atomic.Int64
	importFailures   atomic.Uint64
	importError      atomic.Value
	loginMu          sync.Mutex
	loginWindow      time.Time
	loginAttempts    int
	cacheMu          sync.Mutex
	cache            map[string]cachedAnalytics
	state            *stateStore
	stateReady       atomic.Bool
	stateError       atomic.Value
	checkpoints      *checkpointStore
	checkpointActive atomic.Bool
	lastCheckpoint   atomic.Int64
	checkpointError  atomic.Value
	factClient       *s3.Client
	control          *controlStore
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
	mux.HandleFunc("GET /v1/runtime-restore/{id}", s.bootstrapControls)
	control := s.controlHandler()
	mux.Handle("/v1/auth/", control)
	mux.Handle("/v1/sources", control)
	mux.Handle("/v1/sources/", control)
	mux.Handle("/v1/sub2api/", control)

	mux.HandleFunc("GET /v1/analytics", s.analytics)
	mux.HandleFunc("GET /v1/prices", s.prices)
	mux.HandleFunc("PUT /v1/prices/{model}", s.savePrice)
	mux.HandleFunc("GET /v1/status", s.status)
	return s.authenticate(mux)
}
func (s *Service) health(w http.ResponseWriter, r *http.Request) {
	ready := !s.cfg.RequireInitialImport || (s.lastCollect.Load() > 0 && (s.state == nil || s.stateReady.Load()))
	status, code := "ok", http.StatusOK
	if !ready {
		status, code = "initializing", http.StatusServiceUnavailable
	}
	writeJSON(w, code, map[string]any{"status": status, "revision": s.engine.Revision.Load(), "last_collect_ms": s.lastCollect.Load(), "state_ready": s.state == nil || s.stateReady.Load()})
}
func (s *Service) status(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"status": "ok", "revision": s.engine.Revision.Load(), "active_ingest": s.active.Load(), "last_collect_ms": s.lastCollect.Load(), "remaining_objects": s.remaining.Load(), "import_failures": s.importFailures.Load(), "import_error": s.importError.Load(), "state_ready": s.state == nil || s.stateReady.Load(), "state_error": s.stateError.Load(), "checkpoint_active": s.checkpointActive.Load(), "checkpoint_error": s.checkpointError.Load()})
}
func (s *Service) analytics(w http.ResponseWriter, r *http.Request) {
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
	key := q.Encode() + "|" + strings.Join(allowed, ",")
	rev := s.engine.Revision.Load()
	now := time.Now()
	s.cacheMu.Lock()
	if item, ok := s.cache[key]; ok && item.revision == rev && now.Before(item.expires) {
		body := append([]byte(nil), item.body...)
		s.cacheMu.Unlock()
		s.writeAnalyticsBody(w, r, body)
		return
	}
	s.cacheMu.Unlock()
	result, err := s.engine.Query(r.Context(), QueryFilter{SourceIDs: allowed, SourceID: q.Get("source_id"), Range: name, Model: q.Get("model"), Provider: q.Get("provider"), Endpoint: q.Get("endpoint"), Stream: q.Get("stream"), KeyID: q.Get("key_id"), Timeseries: q.Get("timeseries") == "true"})
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": "analytics unavailable"})
		return
	}
	result.Import = map[string]any{"last_scan_ms": s.lastCollect.Load(), "remaining_objects": s.remaining.Load(), "errors": s.importFailures.Load(), "error_class": s.importError.Load(), "caught_up": s.lastCollect.Load() > 0 && s.remaining.Load() == 0 && (s.importError.Load() == nil || s.importError.Load() == "")}
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
	if err := validatePrice(p); err != nil {
		http.Error(w, "invalid price", 400)
		return
	}
	var err error
	if s.state != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		err = s.state.save(ctx, s.engine, p)
	} else {
		err = s.engine.SavePrice(r.Context(), p)
	}
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

// Warm DuckDB before the TCP readiness probe is exposed. This makes the first
// console click use the same hot page and plan caches as subsequent clicks.
func (s *Service) warmAnalytics(ctx context.Context) {
	for _, name := range []string{"5m", "15m", "1h", "24h", "7d", "30d", "today", "week", "month", "year", "all"} {
		callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, _ = s.engine.Query(callCtx, QueryFilter{Range: name, Timeseries: true})
		cancel()
		if ctx.Err() != nil {
			return
		}
	}
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func (s *Service) Collect(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.Poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.lastCollect.Store(time.Now().UnixMilli())
		}
	}
}
