package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func (s *Service) allowLogin() bool {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	if time.Since(s.loginWindow) > time.Minute {
		s.loginWindow = time.Now()
		s.loginAttempts = 0
	}
	s.loginAttempts++
	return s.loginAttempts <= 20
}
func validateSourceURL(raw string) (string, error) {
	u, e := url.Parse(strings.TrimSpace(raw))
	if e != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("invalid HTTP(S) service address")
	}
	if u.Hostname() == "169.254.169.254" || strings.HasSuffix(u.Hostname(), ".internal") {
		return "", errors.New("metadata endpoints are not allowed")
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && (ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast()) {
		return "", errors.New("invalid address")
	}
	return strings.TrimSuffix(strings.TrimRight(u.String(), "/"), "/v1"), nil
}

var proxyPaths = map[string]bool{"/v1/api-keys": true, "/v1/model-channels": true, "/v1/channel-metrics": true, "/v1/channel-balances": true}
var sourceHTTP = &http.Client{Timeout: 18 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func fetchSource(ctx context.Context, src controlSource, path string, q url.Values) (map[string]any, int, error) {
	if !proxyPaths[path] {
		return nil, 400, errors.New("unsupported observation endpoint")
	}
	u := src.Base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, e := http.NewRequestWithContext(ctx, "GET", u, nil)
	if e != nil {
		return nil, 400, e
	}
	req.Header.Set("Authorization", "Bearer "+src.Key)
	req.Header.Set("Accept", "application/json")
	resp, e := sourceHTTP.Do(req)
	if e != nil {
		return nil, 502, errors.New("source unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, 502, fmt.Errorf("source returned HTTP %d", resp.StatusCode)
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, (16<<20)+1))
	if e != nil || len(raw) > 16<<20 {
		return nil, 502, errors.New("invalid source response")
	}
	var body map[string]any
	if e = json.Unmarshal(raw, &body); e != nil {
		return nil, 502, errors.New("invalid source JSON")
	}
	return body, 200, nil
}
func (s *Service) bootstrapSources(ctx context.Context) error {
	if s.cfg.BootstrapSources == "" {
		return nil
	}
	var sources []controlSource
	if e := json.Unmarshal([]byte(s.cfg.BootstrapSources), &sources); e != nil {
		return errors.New("invalid bootstrap sources JSON")
	}
	for _, src := range sources {
		if src.ID == "" || src.Name == "" || src.Key == "" {
			return errors.New("incomplete bootstrap source")
		}
		base, e := validateSourceURL(src.Base)
		if e != nil {
			return e
		}
		src.Base = base
		if _, e = s.control.saveSource(ctx, src, true); e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) saveSource(w http.ResponseWriter, r *http.Request) {
	var in controlSource
	if !decodeControl(w, r, &in) {
		return
	}
	in.ID = r.PathValue("id")
	if in.ID != "" {
		old, e := s.control.source(r.Context(), in.ID)
		if e != nil {
			http.Error(w, "source not found", 404)
			return
		}
		if in.Key == "" {
			in.Key = old.Key
		}
		if in.Storage.Bucket == "" {
			in.Storage = old.Storage
		}
	}
	if len(in.Name) < 1 || len(in.Name) > 100 || len(in.Key) < 8 || len(in.Key) > 4096 {
		http.Error(w, "invalid source name or platform key", 400)
		return
	}
	base, e := validateSourceURL(in.Base)
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	in.Base = base
	body, _, e := fetchSource(r.Context(), in, "/v1/api-keys", nil)
	if e != nil || body["can_inspect_all"] != true {
		http.Error(w, "source requires a valid platform administrator key", 400)
		return
	}
	if in.Storage.Bucket != "" {
		if in.Storage.AccessKey == "" || in.Storage.SecretKey == "" {
			http.Error(w, "S3 read credentials required", 400)
			return
		}
		if in.Storage.Prefix == "" {
			in.Storage.Prefix = "uni-api-facts/v1/"
		}
		in.Storage.Endpoint, e = validateSourceURL(in.Storage.Endpoint)
		if e != nil {
			http.Error(w, "invalid S3 endpoint", 400)
			return
		}
		client, e := newObjectClient(r.Context(), in.Storage.Endpoint, in.Storage.AccessKey, in.Storage.SecretKey, 10*time.Second)
		if e == nil {
			_, e = client.ListObjectsV2(r.Context(), &s3.ListObjectsV2Input{Bucket: aws.String(in.Storage.Bucket), Prefix: aws.String(in.Storage.Prefix), MaxKeys: aws.Int32(1)})
		}
		if e != nil {
			http.Error(w, "S3 read validation failed", 400)
			return
		}
	}
	out, e := s.control.saveSource(r.Context(), in, false)
	if e != nil {
		http.Error(w, "source storage unavailable", 503)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Service) deleteSource(w http.ResponseWriter, r *http.Request) {
	if e := s.control.deleteSource(r.Context(), r.PathValue("id")); e != nil {
		http.Error(w, "source storage unavailable", 503)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Service) proxySource(w http.ResponseWriter, r *http.Request) {
	path := "/" + r.PathValue("path")
	if !proxyPaths[path] {
		http.Error(w, "unsupported observation endpoint", 400)
		return
	}
	id := r.PathValue("id")
	list, e := s.control.listSources(r.Context())
	if e != nil {
		http.Error(w, "source storage unavailable", 503)
		return
	}
	selected := []sourceView{}
	for _, src := range list {
		if id == "all" || src.ID == id {
			selected = append(selected, src)
		}
	}
	if id != "all" && len(selected) == 0 {
		http.Error(w, "source not found", 404)
		return
	}
	// A key ID is only meaningful in its gateway. The combined directory uses a
	// namespaced selector; never send another source's key to a gateway.
	q := r.URL.Query()
	key := q.Get("api_key_id")
	keySource := ""
	if p := strings.SplitN(key, "::", 2); len(p) == 2 {
		keySource = p[0]
		q.Set("api_key_id", p[1])
	}
	if keySource != "" {
		kept := selected[:0]
		for _, src := range selected {
			if src.ID == keySource {
				kept = append(kept, src)
			}
		}
		selected = kept
	}
	if path == "/v1/channel-balances" && len(selected) != 1 {
		http.Error(w, "select a source for balance queries", 400)
		return
	}
	results := make([]map[string]any, len(selected))
	failures := make([]string, len(selected))
	var wg sync.WaitGroup
	slots := make(chan struct{}, 4)
	for i, view := range selected {
		wg.Add(1)
		go func(i int, view sourceView) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
			case <-r.Context().Done():
				failures[i] = view.Name
				return
			}
			defer func() { <-slots }()
			src, e := s.control.source(r.Context(), view.ID)
			if e != nil {
				failures[i] = view.Name
				return
			}
			body, _, e := fetchSource(r.Context(), src, path, q)
			if e != nil {
				failures[i] = view.Name
				return
			}
			if path == "/v1/api-keys" && body["can_inspect_all"] != true {
				failures[i] = view.Name
				return
			}
			if rows, ok := body["data"].([]any); ok {
				for _, v := range rows {
					row, ok := v.(map[string]any)
					if !ok {
						continue
					}
					row["source_id"] = view.ID
					row["source_name"] = view.Name
					if path == "/v1/api-keys" {
						row["key_id"] = view.ID + "::" + fmt.Sprint(row["key_id"])
					}
				}
			}
			body["source_id"] = view.ID
			body["source_name"] = view.Name
			results[i] = body
		}(i, view)
	}
	wg.Wait()
	failed := []string{}
	data := []any{}
	revisions := []string{}
	var out map[string]any
	for i, body := range results {
		if failures[i] != "" {
			failed = append(failed, failures[i])
			continue
		}
		if body == nil {
			continue
		}
		if out == nil {
			out = body
		}
		if rows, ok := body["data"].([]any); ok {
			data = append(data, rows...)
		}
		revisions = append(revisions, selected[i].ID+":"+fmt.Sprint(body["snapshot_revision"]))
	}
	if len(failed) > 0 && out == nil {
		writeJSON(w, 502, map[string]any{"error": "sources unavailable", "unavailable_sources": failed})
		return
	}
	if out == nil {
		out = map[string]any{}
	}
	if path != "/v1/channel-balances" {
		out["data"] = data
		out["snapshot_revision"] = strings.Join(revisions, ";")
	}
	if path == "/v1/api-keys" {
		out["can_inspect_all"] = true
	}
	out["unavailable_sources"] = failed
	writeJSON(w, 200, out)
}

func (s *Service) analyticsSources(r *http.Request) ([]string, error) {
	if s.control == nil {
		return nil, nil
	}
	list, e := s.control.listSources(r.Context())
	if e != nil {
		return nil, e
	}
	requested := r.URL.Query().Get("source_id")
	if requested == "all" {
		requested = ""
	}
	out := []string{}
	for _, v := range list {
		if requested == "" || requested == v.ID {
			out = append(out, v.ID)
		}
	}
	if requested != "" && len(out) == 0 {
		return nil, sql.ErrNoRows
	}
	return out, nil
}
