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
	"net/http"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"golang.org/x/crypto/bcrypt"
)

// PostgreSQL stores mutable account/source state. DuckDB remains a rebuildable
// cache. No platform key or S3 secret is returned to the browser.
type controlStore struct {
	db  *sql.DB
	key []byte
}
type sourceView struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Base         string `json:"base"`
	CreatedAt    int64  `json:"created_at"`
	HasStorage   bool   `json:"has_storage"`
	HasConfigKey bool   `json:"has_config_key"`
}
type sourceStorage struct {
	Endpoint  string `json:"endpoint"`
	Bucket    string `json:"bucket"`
	Prefix    string `json:"prefix"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
}
type controlSource struct {
	sourceView
	Key            string `json:"key"`
	ConfigKey      string `json:"config_key,omitempty"`
	configKeyError error
	Storage        sourceStorage `json:"storage"`
}

func newControlStore(dsn, master string) (*controlStore, error) {
	if dsn == "" || len(master) < 32 {
		return nil, errors.New("database URL and a master key of at least 32 characters are required")
	}
	sum := sha256.Sum256([]byte(master))
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	_, err = db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS console_users(username TEXT PRIMARY KEY,password_hash TEXT NOT NULL,created_at TIMESTAMPTZ NOT NULL DEFAULT now());
 CREATE TABLE IF NOT EXISTS console_sources(id TEXT PRIMARY KEY,name TEXT NOT NULL,base TEXT NOT NULL,encrypted_key TEXT NOT NULL,created_at TIMESTAMPTZ NOT NULL DEFAULT now());
 ALTER TABLE console_sources ADD COLUMN IF NOT EXISTS encrypted_storage TEXT NOT NULL DEFAULT '';
 ALTER TABLE console_sources ADD COLUMN IF NOT EXISTS encrypted_config_key TEXT NOT NULL DEFAULT '';
 ALTER TABLE console_sources ADD COLUMN IF NOT EXISTS enabled BOOLEAN NOT NULL DEFAULT true;
 CREATE TABLE IF NOT EXISTS console_channel_checks(source_id TEXT NOT NULL REFERENCES console_sources(id),provider TEXT NOT NULL,result JSONB NOT NULL,PRIMARY KEY(source_id,provider));
 CREATE TABLE IF NOT EXISTS console_channel_check_runs(source_id TEXT NOT NULL REFERENCES console_sources(id),provider TEXT NOT NULL,run_id TEXT NOT NULL,expires_at TIMESTAMPTZ NOT NULL,PRIMARY KEY(source_id,provider));
 CREATE TABLE IF NOT EXISTS console_sessions(token_hash TEXT PRIMARY KEY,username TEXT NOT NULL,expires_at TIMESTAMPTZ NOT NULL);
 CREATE INDEX IF NOT EXISTS console_sessions_expiry ON console_sessions(expires_at);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	if _, err = db.ExecContext(ctx, controlPersistenceSchema); err != nil {
		db.Close()
		return nil, err
	}
	if _, err = db.ExecContext(ctx, subSchema); err != nil {
		db.Close()
		return nil, err
	}
	if _, err = db.ExecContext(ctx, subBindingSchema); err != nil {
		db.Close()
		return nil, err
	}
	if _, err = db.ExecContext(ctx, qualityHistorySchema); err != nil {
		db.Close()
		return nil, err
	}
	if _, err = db.ExecContext(ctx, channelSettingsSchema); err != nil {
		db.Close()
		return nil, err
	}
	if _, err = db.ExecContext(ctx, automationSchema); err != nil {
		db.Close()
		return nil, err
	}
	return &controlStore{db: db, key: sum[:]}, nil
}
func (s *controlStore) Close() { _ = s.db.Close() }
func randomID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func (s *controlStore) ensureBootstrap(user, password string) error {
	if user == "" || password == "" {
		return nil
	}
	if len(password) < 12 || len(password) > 72 {
		return errors.New("password must have 12 to 72 bytes")
	}
	// The transaction lock makes one-time initialization safe across replicas.
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`SELECT pg_advisory_xact_lock(8395012)`); err != nil {
		return err
	}
	var n int
	if err = tx.QueryRow(`SELECT count(*) FROM console_users`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO console_users(username,password_hash) VALUES($1,$2)`, user, string(hash)); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *controlStore) authenticate(ctx context.Context, user, password string) bool {
	if len(password) > 72 {
		return false
	}
	var hash string
	if s.db.QueryRowContext(ctx, `SELECT password_hash FROM console_users WHERE username=$1`, user).Scan(&hash) != nil {
		// Equalize expensive work for unknown accounts.
		hash = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
func tokenHash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("%x", sum[:])
}
func (s *controlStore) session(ctx context.Context, raw string) (string, error) {
	if len(raw) != 43 {
		return "", sql.ErrNoRows
	}
	var user string
	err := s.db.QueryRowContext(ctx, `SELECT username FROM console_sessions WHERE token_hash=$1 AND expires_at>now()`, tokenHash(raw)).Scan(&user)
	return user, err
}
func (s *controlStore) newSession(ctx context.Context, user string) (string, error) {
	raw := randomID()
	_, err := s.db.ExecContext(ctx, `INSERT INTO console_sessions(token_hash,username,expires_at) VALUES($1,$2,now()+interval '30 days')`, tokenHash(raw), user)
	return raw, err
}
func (s *controlStore) logout(ctx context.Context, raw string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM console_sessions WHERE token_hash=$1`, tokenHash(raw))
	return err
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
		return "", errors.New("invalid encrypted credential")
	}
	p, err := g.Open(nil, raw[:n], raw[n:], nil)
	return string(p), err
}
func (s *controlStore) listSources(ctx context.Context) ([]sourceView, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,base,extract(epoch FROM created_at)::bigint,(encrypted_storage<>'' OR id='primary'),encrypted_config_key<>'' FROM console_sources WHERE enabled ORDER BY created_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []sourceView{}
	for rows.Next() {
		var v sourceView
		if err = rows.Scan(&v.ID, &v.Name, &v.Base, &v.CreatedAt, &v.HasStorage, &v.HasConfigKey); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *controlStore) source(ctx context.Context, id string) (controlSource, error) {
	var x controlSource
	var key, storage, configKey string
	err := s.db.QueryRowContext(ctx, `SELECT id,name,base,encrypted_key,encrypted_storage,extract(epoch FROM created_at)::bigint,encrypted_config_key FROM console_sources WHERE id=$1 AND enabled`, id).Scan(&x.ID, &x.Name, &x.Base, &key, &storage, &x.CreatedAt, &configKey)
	if err != nil {
		return x, err
	}
	x.Key, err = s.decrypt(key)
	if err != nil {
		return x, err
	}
	if storage != "" {
		raw, e := s.decrypt(storage)
		if e != nil {
			return x, e
		}
		err = json.Unmarshal([]byte(raw), &x.Storage)
	}
	x.HasStorage = storage != "" || id == "primary"
	x.HasConfigKey = configKey != ""
	if x.HasConfigKey {
		// Optional UI access must not block facts, routing controls or boot
		// recovery when its stored credential needs to be replaced.
		x.ConfigKey, x.configKeyError = s.decrypt(configKey)
	}
	return x, err
}
func (s *controlStore) saveSource(ctx context.Context, x controlSource, bootstrap bool) (sourceView, error) {
	enc, err := s.encrypt(x.Key)
	if err != nil {
		return sourceView{}, err
	}
	storage := ""
	configKey := ""
	if x.ConfigKey != "" {
		configKey, err = s.encrypt(x.ConfigKey)
		if err != nil {
			return sourceView{}, err
		}
	}
	if x.Storage.Bucket != "" {
		raw, _ := json.Marshal(x.Storage)
		storage, err = s.encrypt(string(raw))
		if err != nil {
			return sourceView{}, err
		}
	}
	if x.ID == "" {
		x.ID = "src_" + randomID()[:16]
	}
	conflict := `DO UPDATE SET name=EXCLUDED.name,base=EXCLUDED.base,encrypted_key=EXCLUDED.encrypted_key,encrypted_storage=EXCLUDED.encrypted_storage,encrypted_config_key=EXCLUDED.encrypted_config_key`
	if bootstrap {
		conflict = `DO NOTHING`
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO console_sources(id,name,base,encrypted_key,encrypted_storage,encrypted_config_key) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(id) `+conflict, x.ID, x.Name, strings.TrimRight(x.Base, "/"), enc, storage, configKey)
	if err != nil {
		return sourceView{}, err
	}
	if bootstrap {
		return x.sourceView, nil
	}
	out, err := s.source(ctx, x.ID)
	return out.sourceView, err
}
func (s *controlStore) deleteSource(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE console_sources SET enabled=false WHERE id=$1`, id)
	return err
}

func (s *Service) controlHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/auth/me", s.authMe)
	mux.HandleFunc("POST /v1/auth/login", s.authLogin)
	mux.HandleFunc("POST /v1/auth/logout", s.authLogout)
	mux.HandleFunc("PUT /v1/auth/password", s.changePassword)
	mux.HandleFunc("GET /v1/sources", s.sources)
	mux.HandleFunc("POST /v1/sources", s.saveSource)
	mux.HandleFunc("PUT /v1/sources/{id}", s.saveSource)
	mux.HandleFunc("DELETE /v1/sources/{id}", s.deleteSource)
	mux.HandleFunc("GET /v1/sources/{id}/proxy/{path...}", s.proxySource)
	mux.HandleFunc("GET /v1/sources/{id}/control-persistence", s.controlPersistence)
	mux.HandleFunc("PUT /v1/sources/{id}/control-persistence", s.controlPersistence)
	mux.HandleFunc("GET /v1/sources/{id}/channel-controls", s.channelControls)
	mux.HandleFunc("POST /v1/sources/{id}/channel-controls", s.channelControls)
	mux.HandleFunc("GET /v1/sources/{id}/channel-checks", s.channelChecks)
	mux.HandleFunc("GET /v1/sources/{id}/channel-checks/history", s.qualityHistory)
	mux.HandleFunc("GET /v1/sub2api/accounts/{id}/groups/{group}/quality-history", s.qualityHistory)
	mux.HandleFunc("POST /v1/sources/{id}/channel-checks", s.checkChannel)
	mux.HandleFunc("GET /v1/channel-sites", s.channelSites)
	mux.HandleFunc("GET /v1/channel-management", s.channelManagement)
	mux.HandleFunc("POST /v1/channel-management", s.configuredImport)
	mux.HandleFunc("DELETE /v1/channel-management", s.removeConfiguredBinding)
	mux.HandleFunc("GET /v1/sources/{id}/channel-routes", s.channelRoutes)
	mux.HandleFunc("PATCH /v1/sources/{id}/channel-routes", s.editChannelRoutes)
	mux.HandleFunc("GET /v1/channel-spend", s.channelSpend)
	mux.HandleFunc("GET /v1/sources/{id}/channel-info", s.channelInfo)
	mux.HandleFunc("GET /v1/sources/{id}/channel-settings", s.channelSettings)
	mux.HandleFunc("GET /v1/sources/{id}/channel-settings/schema", s.channelSettingsSchema)
	mux.HandleFunc("GET /v1/sources/{id}/channel-settings/secrets", s.channelSettingsSecrets)
	mux.HandleFunc("POST /v1/sources/{id}/channel-settings/validate", s.channelSettingsValidate)
	mux.HandleFunc("POST /v1/sources/{id}/channel-settings/discover", s.channelSettingsDiscover)
	mux.HandleFunc("PATCH /v1/sources/{id}/channel-settings", s.channelSettingsApply)
	mux.HandleFunc("GET /v1/sources/{id}/channel-settings/operations", s.channelSettingsOperations)
	mux.HandleFunc("GET /v1/sources/{id}/channel-settings/operations/{operation}", s.channelSettingsOperation)
	mux.HandleFunc("POST /v1/sources/{id}/channel-settings/rollback", s.channelSettingsRollback)
	mux.HandleFunc("GET /v1/channel-setting-templates", s.channelSettingsTemplates)
	mux.HandleFunc("POST /v1/channel-setting-templates", s.channelSettingsTemplates)

	mux.HandleFunc("GET /v1/sub2api/quality-summary", s.subQualitySummary)
	mux.HandleFunc("GET /v1/sub2api/accounts", s.subAccounts)
	mux.HandleFunc("GET /v1/sub2api/accounts/{id}/balance", s.subAccountBalance)
	mux.HandleFunc("GET /v1/sub2api/accounts/{id}/groups/{group}/spend", s.subChannelSpend)
	mux.HandleFunc("GET /v1/sub2api/accounts/{id}/keys/{key}/spend", s.subAccountKeySpend)
	mux.HandleFunc("POST /v1/sub2api/accounts", s.subAddAccount)
	mux.HandleFunc("POST /v1/sub2api/accounts/sync", s.subSyncAll)
	mux.HandleFunc("DELETE /v1/sub2api/accounts/{id}", s.subDelete)
	mux.HandleFunc("POST /v1/sub2api/accounts/{id}/sync", s.subSync)
	mux.HandleFunc("POST /v1/sub2api/accounts/{id}/stop", s.subStop)
	mux.HandleFunc("POST /v1/sub2api/checks", s.subCheck)
	mux.HandleFunc("POST /v1/sub2api/quality-checks", s.subQualityCheck)
	mux.HandleFunc("POST /v1/sub2api/compaction-checks", s.subCompactionCheck)
	mux.HandleFunc("POST /v1/sub2api/tool-use-checks", s.subToolUseCheck)
	mux.HandleFunc("GET /v1/sub2api/channel-options", s.subChannelOptions)
	mux.HandleFunc("POST /v1/sub2api/channels", s.subImportChannel)
	mux.HandleFunc("GET /v1/sub2api/channels", s.subInstalledChannels)
	mux.HandleFunc("PATCH /v1/sub2api/channels", s.subManageChannel)
	mux.HandleFunc("POST /v1/automations/audit/{audit}/review", s.reviewAutomationSubmission)
	mux.HandleFunc("POST /v1/automations/audit/{audit}/apply", s.applyAutomationSuggestion)
	mux.HandleFunc("POST /v1/automations/audit/{audit}/rollback", s.rollbackAutomation)
	mux.HandleFunc("GET /v1/automations/audit", s.automationAuditHandler)
	mux.HandleFunc("POST /v1/automations/{id}/run", s.queueAutomation)
	mux.HandleFunc("GET /v1/automations", s.automations)
	mux.HandleFunc("POST /v1/automations", s.automations)
	mux.HandleFunc("GET /v1/automations/{id}", s.automations)
	mux.HandleFunc("PUT /v1/automations/{id}", s.automations)
	mux.HandleFunc("DELETE /v1/automations/{id}", s.deleteAutomation)
	return mux
}

type userContextKey struct{}

func (s *Service) controlUser(r *http.Request) (string, error) {
	if s.control == nil {
		return "", sql.ErrNoRows
	}
	c, e := r.Cookie("uni_console_session")
	if e != nil {
		return "", e
	}
	return s.control.session(r.Context(), c.Value)
}
func (s *Service) controlSession(r *http.Request) bool { _, e := s.controlUser(r); return e == nil }
func (s *Service) authMe(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	user, e := s.controlUser(r)
	writeJSON(w, 200, map[string]any{"enabled": s.control != nil, "authenticated": e == nil, "username": user})
}
func decodeControl(w http.ResponseWriter, r *http.Request, v any) bool {
	return decodeControlLimit(w, r, v, 32<<10)
}
func decodeControlLimit(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		http.Error(w, "JSON required", 415)
		return false
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, limit)).Decode(v) != nil {
		http.Error(w, "invalid input", 400)
		return false
	}
	return true
}
func (s *Service) authLogin(w http.ResponseWriter, r *http.Request) {
	if s.control == nil {
		http.Error(w, "unavailable", 503)
		return
	}
	if !s.allowLogin() {
		http.Error(w, "please wait before retrying", 429)
		return
	}
	var in struct{ Username, Password string }
	if !decodeControl(w, r, &in) {
		return
	}
	if !s.control.authenticate(r.Context(), in.Username, in.Password) {
		http.Error(w, "invalid username or password", 401)
		return
	}
	raw, e := s.control.newSession(r.Context(), in.Username)
	if e != nil {
		http.Error(w, "session storage unavailable", 503)
		return
	}
	http.SetCookie(w, s.sessionCookie(raw, 30*86400))
	writeJSON(w, 200, map[string]any{"ok": true, "username": in.Username})
}
func (s *Service) sessionCookie(raw string, age int) *http.Cookie {
	return &http.Cookie{Name: "uni_console_session", Value: raw, Path: "/", HttpOnly: true, Secure: !s.cfg.InsecureCookie, SameSite: http.SameSiteStrictMode, MaxAge: age}
}
func (s *Service) authLogout(w http.ResponseWriter, r *http.Request) {
	if s.control != nil {
		if c, e := r.Cookie("uni_console_session"); e == nil {
			if e = s.control.logout(r.Context(), c.Value); e != nil {
				http.Error(w, "session storage unavailable", 503)
				return
			}
		}
	}
	http.SetCookie(w, s.sessionCookie("", -1))
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Service) changePassword(w http.ResponseWriter, r *http.Request) {
	user, e := s.controlUser(r)
	if e != nil {
		http.Error(w, "unauthorized", 401)
		return
	}
	var in struct{ Current, Password string }
	if !decodeControl(w, r, &in) {
		return
	}
	if len(in.Password) < 12 || len(in.Password) > 72 || !s.control.authenticate(r.Context(), user, in.Current) {
		http.Error(w, "invalid password", 400)
		return
	}
	hash, e := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if e != nil {
		http.Error(w, "invalid password", 400)
		return
	}
	tx, e := s.control.db.BeginTx(r.Context(), nil)
	if e != nil {
		http.Error(w, "storage unavailable", 503)
		return
	}
	defer tx.Rollback()
	_, e = tx.ExecContext(r.Context(), `UPDATE console_users SET password_hash=$1 WHERE username=$2`, string(hash), user)
	if e == nil {
		_, e = tx.ExecContext(r.Context(), `DELETE FROM console_sessions WHERE username=$1`, user)
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		http.Error(w, "storage unavailable", 503)
		return
	}
	http.SetCookie(w, s.sessionCookie("", -1))
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Service) sources(w http.ResponseWriter, r *http.Request) {
	out, e := s.control.listSources(r.Context())
	if e != nil {
		http.Error(w, "storage unavailable", 503)
		return
	}
	writeJSON(w, 200, map[string]any{"data": out})
}
