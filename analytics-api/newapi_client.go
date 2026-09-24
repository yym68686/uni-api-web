package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Detect without transmitting credentials. Legacy sub2api remains the default
// when /api/status is absent; a positive new-api envelope selects its adapter.
func detectSite(ctx context.Context, base string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", base+"/api/status", nil)
	resp, err := subHTTP.Do(req)
	if err != nil {
		return "", errors.New("无法识别站点，请检查站点地址或网络")
	}
	defer resp.Body.Close()
	var status struct {
		Success bool                       `json:"success"`
		Data    map[string]json.RawMessage `json:"data"`
	}
	if resp.StatusCode == 200 && json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&status) == nil && status.Success {
		if status.Data["version"] != nil && (status.Data["quota_per_unit"] != nil || status.Data["system_name"] != nil) {
			return "newapi", nil
		}
	}
	if resp.StatusCode == 429 || resp.StatusCode >= 500 {
		return "", &subRemoteError{Status: resp.StatusCode}
	}
	return "sub2api", nil
}

// Cookie values are encrypted with the account auth. No shared cookie jar and
// no redirects: cookies/bearer credentials can only reach this exact site.
func newAPIJSON(ctx context.Context, base, method, path string, auth *subAuth, body, out any) error {
	var buf io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		buf = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, buf)
	if err != nil {
		return errors.New("站点地址无效")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	origin, _ := url.Parse(base)
	req.Header.Set("Origin", origin.Scheme+"://"+origin.Host)
	if auth != nil {
		if auth.Access != "" {
			req.Header.Set("Authorization", "Bearer "+auth.Access)
		}
		if auth.UserID > 0 {
			req.Header.Set("New-Api-User", strconv.FormatInt(auth.UserID, 10))
		}
		if auth.SessionID != "" {
			req.Header.Set("X-Auth-Session", auth.SessionID)
		}
		for name, value := range auth.Cookies {
			req.AddCookie(&http.Cookie{Name: name, Value: value})
		}
	}
	resp, err := subHTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("无法确认站点响应，请重新同步核对")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil || len(raw) > 4<<20 {
		return errSubResponseTooLarge
	}
	var envelope struct {
		Success *bool           `json:"success"`
		Code    string          `json:"code"`
		Data    json.RawMessage `json:"data"`
	}
	parsed := json.Unmarshal(raw, &envelope)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &subRemoteError{Status: resp.StatusCode, Reason: envelope.Code}
	}
	if parsed != nil || envelope.Success == nil {
		return errors.New("站点返回了不兼容的数据")
	}
	if !*envelope.Success {
		return &subRemoteError{Status: 400, Reason: envelope.Code}
	}
	if auth != nil {
		if auth.Cookies == nil {
			auth.Cookies = map[string]string{}
		}
		for _, c := range resp.Cookies() {
			if c.Domain != "" && !strings.EqualFold(strings.TrimPrefix(c.Domain, "."), origin.Hostname()) {
				continue
			}
			if c.Name != "new_api_refresh" && c.Name != "session" {
				continue
			}
			if c.MaxAge < 0 {
				delete(auth.Cookies, c.Name)
			} else {
				auth.Cookies[c.Name] = c.Value
			}
		}
	}
	if out != nil && (len(envelope.Data) == 0 || json.Unmarshal(envelope.Data, out) != nil) {
		return errors.New("站点返回的数据格式不兼容")
	}
	return nil
}

type newAPIUser struct {
	ID       int64    `json:"id"`
	Username string   `json:"username"`
	Email    string   `json:"email"`
	Group    string   `json:"group"`
	Quota    *float64 `json:"quota"`
}
type newAPILogin struct {
	newAPIUser
	User    newAPIUser `json:"user"`
	Access  string     `json:"access_token"`
	Expires int64      `json:"access_expires_at"`
	Session struct {
		SID string `json:"sid"`
	} `json:"session"`
	Verification bool   `json:"require_verification"`
	Flow         string `json:"flow_token"`
	TwoFA        bool   `json:"require_2fa"`
	Methods      []struct {
		Method string `json:"method"`
	} `json:"methods"`
}

func newAPIApplyLogin(auth *subAuth, login newAPILogin) {
	if login.User.ID > 0 {
		auth.UserID = login.User.ID
		auth.Username = login.User.Username
	} else if login.ID > 0 {
		auth.UserID = login.ID
		auth.Username = login.Username
	}
	auth.Access = login.Access
	auth.ExpiresAt = login.Expires
	auth.SessionID = login.Session.SID
	auth.Kind = "newapi"
}
func newAPIEncryptPassword(password, kid, public string) (string, error) {
	block, _ := pem.Decode([]byte(public))
	if block == nil {
		return "", errors.New("登录加密公钥无效")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return "", errors.New("登录加密公钥无效")
	}
	key, ok := parsed.(*rsa.PublicKey)
	if !ok || key.N.BitLen() < 2048 || key.N.BitLen() > 8192 {
		return "", errors.New("登录加密公钥无效")
	}
	if len(password) <= key.Size()-66 {
		data, e := rsa.EncryptOAEP(sha256.New(), rand.Reader, key, []byte(password), nil)
		return base64.StdEncoding.EncodeToString(data), e
	}
	secret := make([]byte, 32)
	if _, err = rand.Read(secret); err != nil {
		return "", err
	}
	wrapped, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, key, secret, []byte("password-v2"))
	if err != nil {
		return "", err
	}
	cipherBlock, _ := aes.NewCipher(secret)
	gcm, _ := cipher.NewGCM(cipherBlock)
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	data := gcm.Seal(nil, nonce, []byte(password), []byte("password-v2:"+kid))
	enc := base64.StdEncoding.EncodeToString
	return "v2." + enc(wrapped) + "." + enc(nonce) + "." + enc(data), nil
}
func newAPILoginSession(ctx context.Context, base, username, password, flow, code string) (subAuth, newAPILogin, error) {
	auth := subAuth{Kind: "newapi"}
	var login newAPILogin
	if flow != "" {
		var err error
		if strings.HasPrefix(flow, "cookie:") {
			raw, e := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(flow, "cookie:"))
			if e != nil || json.Unmarshal(raw, &auth) != nil {
				return auth, login, errors.New("验证会话无效")
			}
			err = newAPIJSON(ctx, base, "POST", "/api/user/login/2fa", &auth, map[string]string{"code": code}, &login)
		} else {
			err = newAPIJSON(ctx, base, "POST", "/api/user/login/verify", &auth, map[string]string{"flow_token": flow, "method": "2fa", "code": code}, &login)
		}
		if err == nil {
			newAPIApplyLogin(&auth, login)
		}
		return auth, login, err
	}
	if password == "" {
		return auth, login, errors.New("请输入账号密码")
	}
	payload := map[string]string{"username": username, "password": password}
	var crypto struct {
		Enabled bool   `json:"enabled"`
		Kid     string `json:"kid"`
		Public  string `json:"public_key"`
	}
	err := newAPIJSON(ctx, base, "GET", "/api/user/login/encryption-key", nil, nil, &crypto)
	var remote *subRemoteError
	if err != nil && !(errors.As(err, &remote) && remote.Status == 404) {
		return auth, login, err
	}
	if crypto.Enabled {
		encrypted, e := newAPIEncryptPassword(password, crypto.Kid, crypto.Public)
		if e != nil {
			return auth, login, e
		}
		delete(payload, "password")
		payload["password_encrypted"] = encrypted
		payload["encryption_key_id"] = crypto.Kid
	}
	err = newAPIJSON(ctx, base, "POST", "/api/user/login", &auth, payload, &login)
	if err == nil {
		newAPIApplyLogin(&auth, login)
	}
	return auth, login, err
}
func newAPIQuota(ctx context.Context, base string) (float64, error) {
	var status struct {
		Quota float64 `json:"quota_per_unit"`
	}
	if err := newAPIJSON(ctx, base, "GET", "/api/status", nil, nil, &status); err != nil {
		return 0, err
	}
	if status.Quota <= 0 || math.IsNaN(status.Quota) || math.IsInf(status.Quota, 0) {
		return 0, errors.New("站点未提供有效的额度换算单位")
	}
	return status.Quota, nil
}
func (s *Service) newAPIAddAccount(w http.ResponseWriter, r *http.Request, base, username, name, password, flow, code string) {
	if flow != "" && (len(code) != 6 || strings.Trim(code, "0123456789") != "") {
		http.Error(w, "请输入六位验证码", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	auth, login, err := newAPILoginSession(ctx, base, username, password, flow, code)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	owner, _ := s.controlUser(r)
	if login.Verification || login.TwoFA {
		if login.Verification {
			supported := false
			for _, m := range login.Methods {
				if m.Method == "2fa" {
					supported = true
				}
			}
			if !supported || login.Flow == "" {
				http.Error(w, "站点要求 Passkey 等额外验证，请先在原站调整登录验证方式后重试", 400)
				return
			}
			flow = login.Flow
		} else {
			raw, _ := json.Marshal(auth)
			flow = "cookie:" + base64.RawURLEncoding.EncodeToString(raw)
		}
		raw, _ := json.Marshal(subChallenge{Owner: owner, Base: base, Email: username, Name: name, Temp: "newapi:" + flow, Until: time.Now().Add(5 * time.Minute).Unix()})
		encrypted, e := s.control.encrypt(string(raw))
		if e != nil {
			http.Error(w, "验证会话创建失败", 503)
			return
		}
		writeJSON(w, 200, map[string]any{"requires_2fa": true, "challenge": encrypted})
		return
	}
	var user newAPIUser
	if err = newAPIJSON(ctx, base, "GET", "/api/user/self", &auth, nil, &user); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if user.ID <= 0 || !strings.EqualFold(username, user.Username) || (auth.UserID > 0 && auth.UserID != user.ID) {
		http.Error(w, "登录账号与用户名不一致", 400)
		return
	}
	auth.UserID = user.ID
	auth.Username = user.Username
	if auth.Access == "" && len(auth.Cookies) == 0 {
		http.Error(w, "站点未返回登录会话", 400)
		return
	}
	auth.QuotaPerUnit, err = newAPIQuota(ctx, base)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	raw, _ := json.Marshal(auth)
	encrypted, err := s.control.encrypt(string(raw))
	if err != nil {
		http.Error(w, "凭据保存失败", 503)
		return
	}
	// email remains the legacy unique identity column, while login_name is the
	// explicit username used by new accounts and the UI. Never store passwords.
	var id string
	err = s.control.db.QueryRowContext(ctx, `INSERT INTO console_sub_accounts(id,owner,name,base,email,login_name,provider_kind,encrypted_auth,state,job_kind) VALUES($1,$2,$3,$4,$5,$5,'newapi',$6,'queued','sync') ON CONFLICT(owner,base,email) DO UPDATE SET name=excluded.name,login_name=excluded.login_name,provider_kind=excluded.provider_kind,encrypted_auth=excluded.encrypted_auth,state='queued',job_kind='sync',job_id='',message='',lease_until=NULL WHERE console_sub_accounts.state NOT IN ('queued','running') RETURNING id`, "sub_"+randomID()[:16], owner, name, base, user.Username, encrypted).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "该账号已有任务进行中，请等待完成", 409)
		return
	}
	if err != nil {
		http.Error(w, "账号保存失败", 503)
		return
	}
	writeJSON(w, 202, map[string]any{"id": id, "queued": true})
}
func (s *Service) newAPIAuth(ctx context.Context, account string) (subAuth, string, error) {
	var auth subAuth
	var encrypted string
	err := s.control.db.QueryRowContext(ctx, `SELECT encrypted_auth FROM console_sub_accounts WHERE id=$1 AND provider_kind='newapi'`, account).Scan(&encrypted)
	if err != nil {
		return auth, "", err
	}
	raw, e := s.control.decrypt(encrypted)
	if e != nil || json.Unmarshal([]byte(raw), &auth) != nil {
		return auth, "", errors.New("账号凭据无效，请重新登录")
	}
	return auth, encrypted, nil
}
func (s *Service) newAPICall(ctx context.Context, account, base, method, path string, body, out any) error {
	var storedBase string
	if e := s.control.db.QueryRowContext(ctx, `SELECT base FROM console_sub_accounts WHERE id=$1 AND provider_kind='newapi'`, account).Scan(&storedBase); e != nil {
		return e
	}
	if storedBase != base {
		return errors.New("账号与站点地址不匹配")
	}

	auth, encrypted, err := s.newAPIAuth(ctx, account)
	if err != nil {
		return err
	}
	err = newAPIJSON(ctx, base, method, path, &auth, body, out)
	var remote *subRemoteError
	if !errors.As(err, &remote) || remote.Status != 401 {
		return err
	}
	// Only rejected reads (including explicit key reveal) are safe to repeat.
	if method != "GET" && !strings.HasSuffix(path, "/key") {
		return err
	}
	lockCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	unlock, e := s.subLockAuth(lockCtx, account)
	if e != nil {
		return e
	}
	defer unlock()
	next, current, e := s.newAPIAuth(lockCtx, account)
	if e != nil {
		return e
	}
	if current == encrypted {
		if next.Cookies["new_api_refresh"] == "" {
			return errors.New("站点会话已失效，请重新登录")
		}
		var login newAPILogin
		if e = newAPIJSON(lockCtx, base, "POST", "/api/user/auth/refresh", &next, nil, &login); e != nil {
			return e
		}
		if login.Access == "" || login.User.ID != next.UserID {
			return errors.New("刷新会话身份不匹配，请重新登录")
		}
		newAPIApplyLogin(&next, login)
		raw, _ := json.Marshal(next)
		encoded, e := s.control.encrypt(string(raw))
		if e != nil {
			return e
		}
		res, e := s.control.db.ExecContext(lockCtx, `UPDATE console_sub_accounts SET encrypted_auth=$3 WHERE id=$1 AND encrypted_auth=$2`, account, current, encoded)
		if e != nil {
			return e
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return errors.New("账号会话已更新，请重试")
		}
	}
	return newAPIJSON(ctx, base, method, path, &next, body, out)
}
func (s *Service) isNewAPI(ctx context.Context, account string) bool {
	var kind string
	_ = s.control.db.QueryRowContext(ctx, `SELECT provider_kind FROM console_sub_accounts WHERE id=$1`, account).Scan(&kind)
	return kind == "newapi"
}
