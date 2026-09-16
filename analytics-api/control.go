package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	_ "github.com/jackc/pgx/v5/stdlib"
	"golang.org/x/crypto/bcrypt"
	"io"
	"net/http"
	"strings"
	"time"
)

type controlStore struct {
	db  *sql.DB
	key []byte
}
type sourceView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Base      string `json:"base"`
	CreatedAt int64  `json:"created_at"`
}
type controlSource struct {
	ID, Name, Base, Key string
	CreatedAt           int64
}
type controlPlane interface {
	Close()
	ensureBootstrap(string, string) error
	countUsers() (int, error)
	authenticate(string, string) bool
	session(string) bool
	newSession(string) string
	logout(string)
	listSources() ([]sourceView, error)
	source(string) (controlSource, bool)
	addSource(string, string, string) (sourceView, error)
}

func newControlStore(dsn, master string) (*controlStore, error) {
	if strings.TrimSpace(dsn) == "" || strings.TrimSpace(master) == "" {
		return nil, errors.New("CONTROL_DATABASE_URL and CONTROL_MASTER_KEY are required")
	}
	sum := sha256.Sum256([]byte(master))
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	s := &controlStore{db: db, key: sum[:]}
	_, err = db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS console_users(username TEXT PRIMARY KEY,password_hash TEXT NOT NULL,created_at TIMESTAMPTZ NOT NULL DEFAULT now()); CREATE TABLE IF NOT EXISTS console_sources(id TEXT PRIMARY KEY,name TEXT NOT NULL,base TEXT NOT NULL,encrypted_key TEXT NOT NULL,created_at TIMESTAMPTZ NOT NULL DEFAULT now()); CREATE TABLE IF NOT EXISTS console_sessions(token_hash TEXT PRIMARY KEY,username TEXT NOT NULL,expires_at TIMESTAMPTZ NOT NULL); CREATE INDEX IF NOT EXISTS console_sessions_expiry ON console_sessions(expires_at);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *controlStore) Close() {
	if s != nil && s.db != nil {
		_ = s.db.Close()
	}
}
func (s *controlStore) ensureBootstrap(user, password string) error {
	if user == "" || password == "" {
		return nil
	}
	if len(password) < 12 {
		return errors.New("ADMIN_PASSWORD must be at least 12 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO console_users(username,password_hash) VALUES($1,$2) ON CONFLICT(username) DO NOTHING`, user, string(hash))
	return err
}
func (s *controlStore) countUsers() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT count(*) FROM console_users`).Scan(&n)
	return n, err
}
func (s *controlStore) authenticate(user, password string) bool {
	var hash string
	if s.db.QueryRow(`SELECT password_hash FROM console_users WHERE username=$1`, user).Scan(&hash) != nil {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
func (s *controlStore) session(raw string) bool {
	if raw == "" {
		return false
	}
	sum := sha256.Sum256([]byte(raw))
	var n int
	err := s.db.QueryRow(`SELECT 1 FROM console_sessions WHERE token_hash=$1 AND expires_at>now()`, fmt.Sprintf("%x", sum[:])).Scan(&n)
	return err == nil
}
func (s *controlStore) newSession(user string) string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	raw := base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(raw))
	_, _ = s.db.Exec(`DELETE FROM console_sessions WHERE expires_at<=now()`)
	_, _ = s.db.Exec(`INSERT INTO console_sessions(token_hash,username,expires_at) VALUES($1,$2,now()+interval '12 hours')`, fmt.Sprintf("%x", sum[:]), user)
	return raw
}
func (s *controlStore) logout(raw string) {
	sum := sha256.Sum256([]byte(raw))
	_, _ = s.db.Exec(`DELETE FROM console_sessions WHERE token_hash=$1`, fmt.Sprintf("%x", sum[:]))
}
func (s *controlStore) encrypt(plain string) (string, error) {
	b, err := aes.NewCipher(s.key)
	if err != nil {
		return "", err
	}
	g, err := cipher.NewGCM(b)
	if err != nil {
		return "", err
	}
	n := make([]byte, g.NonceSize())
	if _, err = rand.Read(n); err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(g.Seal(n, n, []byte(plain), nil)), nil
}
func (s *controlStore) decrypt(v string) (string, error) {
	raw, err := base64.RawStdEncoding.DecodeString(v)
	if err != nil {
		return "", err
	}
	b, err := aes.NewCipher(s.key)
	if err != nil {
		return "", err
	}
	g, err := cipher.NewGCM(b)
	if err != nil {
		return "", err
	}
	n := g.NonceSize()
	if len(raw) < n {
		return "", errors.New("invalid encrypted source key")
	}
	p, err := g.Open(nil, raw[:n], raw[n:], nil)
	return string(p), err
}
func (s *controlStore) listSources() ([]sourceView, error) {
	rows, err := s.db.Query(`SELECT id,name,base,extract(epoch FROM created_at)::bigint FROM console_sources ORDER BY created_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []sourceView{}
	for rows.Next() {
		var v sourceView
		if err = rows.Scan(&v.ID, &v.Name, &v.Base, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *controlStore) source(id string) (controlSource, bool) {
	var x controlSource
	var enc string
	if err := s.db.QueryRow(`SELECT id,name,base,encrypted_key,extract(epoch FROM created_at)::bigint FROM console_sources WHERE id=$1`, id).Scan(&x.ID, &x.Name, &x.Base, &enc, &x.CreatedAt); err != nil {
		return controlSource{}, false
	}
	x.Key, _ = s.decrypt(enc)
	return x, x.Key != ""
}
func (s *controlStore) addSource(name, base, key string) (sourceView, error) {
	enc, err := s.encrypt(key)
	if err != nil {
		return sourceView{}, err
	}
	b := make([]byte, 9)
	if _, err = rand.Read(b); err != nil {
		return sourceView{}, err
	}
	id := "src_" + base64.RawURLEncoding.EncodeToString(b)
	var v sourceView
	err = s.db.QueryRow(`INSERT INTO console_sources(id,name,base,encrypted_key) VALUES($1,$2,$3,$4) RETURNING id,name,base,extract(epoch FROM created_at)::bigint`, id, name, strings.TrimRight(base, "/"), enc).Scan(&v.ID, &v.Name, &v.Base, &v.CreatedAt)
	return v, err
}
func (s *Service) controlHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/auth/me", s.authMe)
	mux.HandleFunc("POST /v1/auth/setup", s.authSetup)
	mux.HandleFunc("POST /v1/auth/login", s.authLogin)
	mux.HandleFunc("POST /v1/auth/logout", s.authLogout)
	mux.HandleFunc("GET /v1/sources", s.sources)
	mux.HandleFunc("POST /v1/sources", s.addSource)
	mux.HandleFunc("GET /v1/sources/{id}/proxy/{path...}", s.proxySource)
	return mux
}
func (s *Service) controlSession(r *http.Request) bool {
	if s.control == nil {
		return false
	}
	c, e := r.Cookie("uni_console_session")
	return e == nil && s.control.session(c.Value)
}
func (s *Service) authMe(w http.ResponseWriter, r *http.Request) {
	if s.control == nil {
		writeJSON(w, 503, map[string]string{"error": "control plane unavailable"})
		return
	}
	n, e := s.control.countUsers()
	if e != nil {
		http.Error(w, "control plane unavailable", 503)
		return
	}
	writeJSON(w, 200, map[string]any{"authenticated": s.controlSession(r), "setup_required": n == 0})
}
func (s *Service) authSetup(w http.ResponseWriter, r *http.Request) {
	if s.control == nil {
		http.Error(w, "unavailable", 503)
		return
	}
	n, e := s.control.countUsers()
	if e != nil || n > 0 {
		http.Error(w, "setup unavailable", 409)
		return
	}
	var in struct{ Username, Password string }
	if json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&in) != nil || len(in.Username) < 2 || len(in.Password) < 12 {
		http.Error(w, "invalid credentials", 400)
		return
	}
	if e = s.control.ensureBootstrap(in.Username, in.Password); e != nil {
		http.Error(w, "setup failed", 500)
		return
	}
	s.setControlCookie(w, in.Username)
	writeJSON(w, 201, map[string]any{"ok": true})
}
func (s *Service) authLogin(w http.ResponseWriter, r *http.Request) {
	if s.control == nil {
		http.Error(w, "unavailable", 503)
		return
	}
	var in struct{ Username, Password string }
	if json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&in) != nil || !s.control.authenticate(in.Username, in.Password) {
		http.Error(w, "invalid username or password", 401)
		return
	}
	s.setControlCookie(w, in.Username)
	sources, _ := s.control.listSources()
	writeJSON(w, 200, map[string]any{"ok": true, "sources": sources})
}
func (s *Service) setControlCookie(w http.ResponseWriter, user string) {
	http.SetCookie(w, &http.Cookie{Name: "uni_console_session", Value: s.control.newSession(user), Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: 43200})
}
func (s *Service) authLogout(w http.ResponseWriter, r *http.Request) {
	if c, e := r.Cookie("uni_console_session"); e == nil {
		s.control.logout(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: "uni_console_session", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	writeJSON(w, 200, map[string]any{"ok": true})
}
func (s *Service) sources(w http.ResponseWriter, r *http.Request) {
	if !s.controlSession(r) {
		http.Error(w, "unauthorized", 401)
		return
	}
	out, e := s.control.listSources()
	if e != nil {
		http.Error(w, "control plane unavailable", 503)
		return
	}
	writeJSON(w, 200, map[string]any{"data": out})
}
func (s *Service) addSource(w http.ResponseWriter, r *http.Request) {
	if !s.controlSession(r) {
		http.Error(w, "unauthorized", 401)
		return
	}
	var in struct{ Name, Base, Key string }
	if json.NewDecoder(io.LimitReader(r.Body, 32<<10)).Decode(&in) != nil || strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.Base) == "" || strings.TrimSpace(in.Key) == "" {
		http.Error(w, "invalid source", 400)
		return
	}
	if _, e := s.control.addSource(in.Name, in.Base, in.Key); e != nil {
		http.Error(w, "source storage failed", 500)
		return
	}
	writeJSON(w, 201, map[string]any{"ok": true})
}
func (s *Service) proxySource(w http.ResponseWriter, r *http.Request) {
	if !s.controlSession(r) {
		http.Error(w, "unauthorized", 401)
		return
	}
	if r.PathValue("id") == "all" {
		s.proxyAllSources(w, r)
		return
	}
	src, ok := s.control.source(r.PathValue("id"))
	if !ok {
		http.Error(w, "source not found", 404)
		return
	}
	path := "/" + strings.TrimPrefix(r.PathValue("path"), "/")
	if path == "/" || strings.Contains(path, "..") || !strings.HasPrefix(path, "/v1/") {
		http.Error(w, "invalid path", 400)
		return
	}
	u := strings.TrimRight(src.Base, "/") + path
	if r.URL.RawQuery != "" {
		u += "?" + r.URL.RawQuery
	}
	req, e := http.NewRequestWithContext(r.Context(), r.Method, u, r.Body)
	if e != nil {
		http.Error(w, "invalid upstream", 400)
		return
	}
	req.Header.Set("Authorization", "Bearer "+src.Key)
	req.Header.Set("Accept", "application/json")
	for _, h := range []string{"Content-Type", "Accept-Encoding"} {
		if v := r.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	client := &http.Client{Timeout: 25 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, e := client.Do(req)
	if e != nil {
		http.Error(w, "source unavailable", 503)
		return
	}
	defer resp.Body.Close()
	for _, h := range []string{"Content-Type", "Content-Encoding"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, 16<<20))
}

func (s *Service) proxyAllSources(w http.ResponseWriter, r *http.Request) {
	sources, err := s.control.listSources()
	if err != nil || len(sources) == 0 {
		http.Error(w, "no sources configured", 503)
		return
	}
	path := "/" + strings.TrimPrefix(r.PathValue("path"), "/")
	if path == "/" || strings.Contains(path, "..") || !strings.HasPrefix(path, "/v1/") {
		http.Error(w, "invalid path", 400)
		return
	}
	var merged map[string]any
	for _, view := range sources {
		src, ok := s.control.source(view.ID)
		if !ok {
			continue
		}
		u := strings.TrimRight(src.Base, "/") + path
		if r.URL.RawQuery != "" {
			u += "?" + r.URL.RawQuery
		}
		req, e := http.NewRequestWithContext(r.Context(), r.Method, u, r.Body)
		if e != nil {
			continue
		}
		req.Header.Set("Authorization", "Bearer "+src.Key)
		req.Header.Set("Accept", "application/json")
		resp, e := (&http.Client{Timeout: 25 * time.Second}).Do(req)
		if e != nil || resp.StatusCode >= 400 {
			if resp != nil {
				resp.Body.Close()
			}
			continue
		}
		var body map[string]any
		e = json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&body)
		resp.Body.Close()
		if e != nil {
			continue
		}
		if merged == nil {
			merged = body
			continue
		}
		if a, ok := body["data"].([]any); ok {
			if existing, ok := merged["data"].([]any); ok {
				merged["data"] = append(existing, a...)
			}
		}
	}
	if merged == nil {
		http.Error(w, "sources unavailable", 503)
		return
	}
	writeJSON(w, 200, merged)
}
