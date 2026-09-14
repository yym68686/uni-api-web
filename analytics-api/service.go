package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"time"
)

type Service struct {
	auth        *authorizer
	engine      *Engine
	cfg         Config
	active      atomic.Int64
	lastCollect atomic.Int64
}

func NewService(e *Engine, cfg Config) (*Service, error) {
	if e == nil {
		return nil, errors.New("engine required")
	}
	auth, err := newAuthorizer(cfg.Upstream)
	if err != nil {
		return nil, err
	}
	return &Service{engine: e, cfg: cfg, auth: auth}, nil
}
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)

	mux.HandleFunc("GET /v1/analytics", s.analytics)
	mux.HandleFunc("GET /v1/prices", s.prices)
	mux.HandleFunc("PUT /v1/prices/{model}", s.savePrice)
	mux.HandleFunc("GET /v1/status", s.status)
	return s.authenticate(mux)
}
func (s *Service) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"status": "ok", "revision": s.engine.Revision.Load(), "last_collect_ms": s.lastCollect.Load()})
}
func (s *Service) status(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"status": "ok", "revision": s.engine.Revision.Load(), "active_ingest": s.active.Load(), "last_collect_ms": s.lastCollect.Load()})
}
func (s *Service) analytics(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	name := q.Get("range")
	if name == "" {
		name = "15m"
	}
	result, err := s.engine.Query(r.Context(), QueryFilter{Range: name, Model: q.Get("model"), Provider: q.Get("provider"), Endpoint: q.Get("endpoint"), Stream: q.Get("stream")})
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": "analytics unavailable"})
		return
	}
	writeJSON(w, 200, result)
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
	if err := s.engine.SavePrice(r.Context(), p); err != nil {
		http.Error(w, "invalid price", 400)
		return
	}
	writeJSON(w, 200, map[string]any{"price": p})
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
