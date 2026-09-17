package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type authCacheEntry struct {
	Until time.Time
	Keys  json.RawMessage
}
type authorizer struct {
	upstream string
	client   *http.Client
	mu       sync.Mutex
	cache    map[string]authCacheEntry
}

func newAuthorizer(upstream string) (*authorizer, error) {
	u, err := url.Parse(upstream)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("invalid configured upstream")
	}
	return &authorizer{upstream: strings.TrimRight(upstream, "/"), client: &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, cache: map[string]authCacheEntry{}}, nil
}
func (a *authorizer) verify(ctx context.Context, header string) (json.RawMessage, int, error) {
	if !strings.HasPrefix(header, "Bearer ") || len(header) < 12 || len(header) > 4096 {
		return nil, 401, errors.New("administrator key required")
	}
	digest := sha256.Sum256([]byte(header))
	id := hex.EncodeToString(digest[:])
	now := time.Now()
	a.mu.Lock()
	cached, ok := a.cache[id]
	a.mu.Unlock()
	if ok && now.Before(cached.Until) {
		return cached.Keys, 200, nil
	}
	req, err := http.NewRequestWithContext(ctx, "GET", a.upstream+"/v1/api-keys", nil)
	if err != nil {
		return nil, 503, err
	}
	req.Header.Set("Authorization", header)
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, 503, errors.New("uni-api authorization unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		status := 503
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			status = resp.StatusCode
		}
		return nil, status, errors.New("uni-api administrator authorization failed")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, 503, err
	}
	var doc struct {
		CanInspectAll bool `json:"can_inspect_all"`
	}
	if err = json.Unmarshal(raw, &doc); err != nil || !doc.CanInspectAll {
		return nil, 403, errors.New("uni-api administrator key required")
	}
	a.mu.Lock()
	if len(a.cache) > 128 {
		a.cache = map[string]authCacheEntry{}
	}
	a.cache[id] = authCacheEntry{Until: now.Add(30 * time.Second), Keys: raw}
	a.mu.Unlock()
	return raw, 200, nil
}
func (s *Service) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		if s.control != nil {
			if r.Method != "GET" && r.Method != "HEAD" {
				if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
					http.Error(w, "cross-site request rejected", 403)
					return
				}
				if origin := r.Header.Get("Origin"); origin != "" {
					u, e := url.Parse(origin)
					host := r.Host
					if forwarded := r.Header.Get("X-Forwarded-Host"); forwarded != "" {
						host = forwarded
					}
					if s.cfg.PublicOrigin != "" {
						expected, err := url.Parse(s.cfg.PublicOrigin)
						if err != nil || e != nil || u.Scheme != expected.Scheme || u.Host != expected.Host {
							http.Error(w, "cross-site request rejected", 403)
							return
						}
					} else if e != nil || u.Host != host {
						http.Error(w, "cross-site request rejected", 403)
						return
					}
				}
			}
			if r.URL.Path == "/v1/auth/me" || r.URL.Path == "/v1/auth/login" {
				next.ServeHTTP(w, r)
				return
			}
			if !s.controlSession(r) {
				http.Error(w, "login required", 401)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/auth/") {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/sources") {
			http.Error(w, "account service unavailable", 503)
			return
		}
		_, status, err := s.auth.verify(r.Context(), r.Header.Get("Authorization"))
		if err != nil {
			writeJSON(w, status, map[string]string{"error": err.Error()})
			return
		}
		next.ServeHTTP(w, r)
	})
}
