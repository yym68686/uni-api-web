package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type rechargeConfig struct {
	Enabled     bool     `json:"enabled"`
	ShopURL     string   `json:"shop_url"`
	Products    []string `json:"products"`
	Contact     string   `json:"contact"`
	MaxCNY      string   `json:"max_cny"`
	TargetBase  string   `json:"target_base"`
	TargetEmail string   `json:"target_email"`
}
type rechargeQuote struct {
	ID       string `json:"id"`
	Account  string `json:"account_id"`
	Product  string `json:"product"`
	Name     string `json:"name"`
	Merchant string `json:"merchant"`
	Total    string `json:"total_cny"`
	Fee      string `json:"fee_cny"`
	Target   string `json:"target"`
	Expires  int64  `json:"expires_at"`
}

// All authorization, payment and delivery material is encrypted at rest and
// excluded from GET responses, request logs, quotes, events and error strings.
type rechargeContext struct {
	Config     rechargeConfig `json:"config"`
	UserID     int64          `json:"user_id"`
	Channel    int            `json:"channel"`
	Total      rechargeMoney  `json:"total_fen"`
	Trade      string         `json:"trade"`
	Visitor    string         `json:"visitor"`
	Password   string         `json:"password"`
	PaymentURL string         `json:"payment_url"`
	ActionURL  string         `json:"action_url"`
	Code       string         `json:"code"`
	Credit     *float64       `json:"credit,omitempty"`
}

// Money's wire representation inside the encrypted context is integer fen,
// not the merchant's yuan JSON decoder.
func (c rechargeContext) marshal() ([]byte, error) {
	type alias rechargeContext
	return json.Marshal(struct {
		alias
		Fen int64 `json:"total_fen"`
	}{alias(c), int64(c.Total)})
}
func decodeRechargeContext(raw string) (rechargeContext, error) {
	type alias rechargeContext
	var wire struct {
		alias
		Fen int64 `json:"total_fen"`
	}
	err := json.Unmarshal([]byte(raw), &wire)
	c := rechargeContext(wire.alias)
	c.Total = rechargeMoney(wire.Fen)
	return c, err
}

type rechargeJob struct {
	ID                    string        `json:"id"`
	State                 string        `json:"state"`
	Message               string        `json:"message"`
	Quote                 rechargeQuote `json:"quote"`
	Created               int64         `json:"created_at"`
	Updated               int64         `json:"updated_at"`
	ActionURL             string        `json:"action_url,omitempty"`
	Credit                *float64      `json:"credited_amount,omitempty"`
	MerchantOrder         string        `json:"merchant_order,omitempty"`
	owner, account, lease string
	context               rechargeContext
}

func (s *Service) rechargeAccount(ctx context.Context, owner, id string) (base, email, kind string, err error) {
	err = s.control.db.QueryRowContext(ctx, `SELECT base,email,provider_kind FROM console_sub_accounts WHERE id=$1 AND owner=$2`, id, owner).Scan(&base, &email, &kind)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		err = errors.New("站点账号读取失败")
	}
	return
}
func rechargeError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "站点账号或充值记录不存在", 404)
		return
	}
	// All remote clients return fixed, non-reflective messages.
	http.Error(w, err.Error(), http.StatusConflict)
}
func (s *Service) rechargeSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	owner, err := s.controlUser(r)
	if err != nil {
		http.Error(w, "unauthorized", 401)
		return
	}
	id := r.PathValue("id")
	base, email, kind, err := s.rechargeAccount(r.Context(), owner, id)
	if err != nil {
		rechargeError(w, err)
		return
	}
	if r.Method == "GET" {
		cfg, revision, err := s.readRechargeConfig(r.Context(), id)
		if err != nil {
			rechargeError(w, errors.New("充值设置读取失败"))
			return
		}
		writeJSON(w, 200, map[string]any{"config": cfg, "revision": revision, "supported": kind == "" || kind == "sub2api", "payment_configured": s.rechargePayments().Configured(owner), "identity_current": cfg.TargetBase == "" || (cfg.TargetBase == base && strings.EqualFold(cfg.TargetEmail, email))})
		return
	}
	var cfg rechargeConfig
	if !decodeControl(w, r, &cfg) {
		return
	}
	if kind != "" && kind != "sub2api" {
		rechargeError(w, errors.New("目前仅适配 sub2api + 链动充值，其他站点不会套用此流程"))
		return
	}
	if cfg.ShopURL != "" {
		_, canonical, e := parseRechargeShop(cfg.ShopURL)
		if e != nil {
			rechargeError(w, e)
			return
		}
		cfg.ShopURL = canonical
	}
	if len(cfg.Products) > 30 || len(cfg.Contact) > 200 || strings.ContainsAny(cfg.Contact, "\r\n\x00") {
		rechargeError(w, errors.New("充值商品或联系方式无效"))
		return
	}
	keys := map[string]bool{}
	for _, key := range cfg.Products {
		if !rechargeKey.MatchString(key) || keys[key] {
			rechargeError(w, errors.New("充值商品编号无效或重复"))
			return
		}
		keys[key] = true
	}
	var max rechargeMoney
	if max.UnmarshalJSON([]byte(cfg.MaxCNY)) != nil || max <= 0 || max > 1000000 {
		rechargeError(w, errors.New("单次充值含手续费上限须大于 0 且不超过 10000 元"))
		return
	}
	if cfg.Enabled && (cfg.ShopURL == "" || len(cfg.Products) == 0 || strings.TrimSpace(cfg.Contact) == "" || cfg.TargetBase != base || !strings.EqualFold(cfg.TargetEmail, email)) {
		rechargeError(w, errors.New("请配置店铺、充值商品、联系方式，并确认充值到当前站点账号"))
		return
	}
	cfg.MaxCNY = max.String()
	if cfg.Products == nil {
		cfg.Products = []string{}
	}
	cfg.TargetBase = base
	cfg.TargetEmail = email
	raw, _ := json.Marshal(cfg)
	_, err = s.control.db.ExecContext(r.Context(), `INSERT INTO console_recharge_settings(account_id,revision,config) VALUES($1,$2,$3) ON CONFLICT(account_id) DO UPDATE SET revision=excluded.revision,config=excluded.config,updated_at=now()`, id, randomID(), string(raw))
	if err != nil {
		rechargeError(w, errors.New("充值设置保存失败"))
		return
	}
	writeJSON(w, 200, map[string]bool{"saved": true})
}
func (s *Service) readRechargeConfig(ctx context.Context, id string) (rechargeConfig, string, error) {
	cfg := rechargeConfig{MaxCNY: "100.00", Products: []string{}}
	var revision string
	var raw []byte
	err := s.control.db.QueryRowContext(ctx, `SELECT revision,config FROM console_recharge_settings WHERE account_id=$1`, id).Scan(&revision, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return cfg, "", nil
	}
	if err == nil {
		err = json.Unmarshal(raw, &cfg)
	}
	return cfg, revision, err
}
func (s *Service) rechargeCatalog(w http.ResponseWriter, r *http.Request) {
	owner, err := s.controlUser(r)
	if err != nil {
		http.Error(w, "unauthorized", 401)
		return
	}
	if _, _, _, err = s.rechargeAccount(r.Context(), owner, r.PathValue("id")); err != nil {
		rechargeError(w, err)
		return
	}
	var in struct {
		ShopURL string `json:"shop_url"`
	}
	if !decodeControl(w, r, &in) {
		return
	}
	shop, canonical, err := parseRechargeShop(in.ShopURL)
	if err != nil {
		rechargeError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	products, err := shop.catalog(ctx, subHTTP)
	if err != nil {
		rechargeError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"shop_url": canonical, "products": products})
}
func (s *Service) rechargeWalletHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	owner, err := s.controlUser(r)
	if err != nil {
		http.Error(w, "unauthorized", 401)
		return
	}
	if _, _, _, err = s.rechargeAccount(r.Context(), owner, r.PathValue("id")); err != nil {
		rechargeError(w, err)
		return
	}
	pay := s.rechargePayments()
	if !pay.Configured(owner) {
		writeJSON(w, 200, rechargeWallet{Status: "unconfigured", Message: "支付代理未配置：须由管理员安装支付宝官方 CLI，并为当前用户绑定隔离的钱包和运行时会话"})
		return
	}
	action, code := "status", ""
	if r.Method == "POST" {
		var input struct {
			Action string `json:"action"`
			Code   string `json:"code"`
		}
		if !decodeControl(w, r, &input) {
			return
		}
		action, code = input.Action, input.Code
	}
	ctx, cancel := context.WithTimeout(r.Context(), 50*time.Second)
	defer cancel()
	unlock, err := s.rechargeWalletLock(ctx, owner)
	if err != nil {
		rechargeError(w, err)
		return
	}
	defer unlock()
	status, err := pay.Wallet(ctx, owner, action, code)
	if err != nil {
		rechargeError(w, err)
		return
	}
	writeJSON(w, 200, status)
}

type rechargeProfile struct {
	ID    int64  `json:"id"`
	Email string `json:"email"`
}

func (s *Service) rechargeIdentity(ctx context.Context, owner, account string, cfg rechargeConfig, expected int64) (string, rechargeProfile, error) {
	var profile rechargeProfile
	base, email, kind, err := s.rechargeAccount(ctx, owner, account)
	if err != nil {
		return "", profile, err
	}
	if (kind != "" && kind != "sub2api") || base != cfg.TargetBase || !strings.EqualFold(email, cfg.TargetEmail) {
		return "", profile, errors.New("充值目标账号已变化，已停止自动付款或兑换，请核对原订单")
	}
	token, err := s.subUsageAuth(ctx, account, base, "")
	if err != nil {
		return "", profile, errors.New("站点登录不可用，请重新登录后核对原充值")
	}
	err = subJSON(ctx, subHTTP, base, "GET", "/api/v1/auth/me", token, nil, &profile, "")
	var remote *subRemoteError
	if errors.As(err, &remote) && remote.Status == 401 {
		token, err = s.subUsageAuth(ctx, account, base, token)
		if err == nil {
			err = subJSON(ctx, subHTTP, base, "GET", "/api/v1/auth/me", token, nil, &profile, "")
		}
	}
	if err != nil {
		return "", profile, errors.New("无法确认充值目标登录身份；未发起新的付款或兑换")
	}
	if profile.ID <= 0 || !strings.EqualFold(profile.Email, email) || (expected > 0 && expected != profile.ID) {
		return "", profile, errors.New("站点返回的充值账号身份不匹配；已阻止操作")
	}
	return token, profile, nil
}
func (s *Service) rechargeQuoteHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	owner, err := s.controlUser(r)
	if err != nil {
		http.Error(w, "unauthorized", 401)
		return
	}
	account := r.PathValue("id")
	if _, _, _, err = s.rechargeAccount(r.Context(), owner, account); err != nil {
		rechargeError(w, err)
		return
	}
	var in struct {
		Product string `json:"product"`
	}
	if !decodeControl(w, r, &in) {
		return
	}
	cfg, revision, err := s.readRechargeConfig(r.Context(), account)
	if err != nil {
		rechargeError(w, errors.New("充值配置读取失败"))
		return
	}
	allowed := false
	for _, key := range cfg.Products {
		allowed = allowed || key == in.Product
	}
	if !cfg.Enabled || !allowed {
		rechargeError(w, errors.New("该充值商品尚未在站点设置中启用"))
		return
	}
	if !s.rechargePayments().Configured(owner) {
		rechargeError(w, errors.New("支付代理尚未配置，未创建订单或付款"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	unlock, err := s.rechargeWalletLock(ctx, owner)
	if err != nil {
		rechargeError(w, err)
		return
	}
	defer unlock()
	wallet, err := s.rechargePayments().Wallet(ctx, owner, "status", "")
	if err != nil || wallet.Status != "bound" {
		rechargeError(w, errors.New("支付宝钱包尚未就绪，请在充值设置中完成官方授权；未创建订单"))
		return
	}
	_, profile, err := s.rechargeIdentity(ctx, owner, account, cfg, 0)
	if err != nil {
		rechargeError(w, err)
		return
	}
	shop, _, err := parseRechargeShop(cfg.ShopURL)
	if err != nil {
		rechargeError(w, err)
		return
	}
	goods, err := shop.goods(ctx, subHTTP, in.Product)
	if err != nil {
		rechargeError(w, err)
		return
	}
	channel, err := shop.channel(ctx, subHTTP)
	if err != nil {
		rechargeError(w, err)
		return
	}
	price, err := shop.price(ctx, subHTTP, in.Product, channel)
	if err != nil {
		rechargeError(w, err)
		return
	}
	var maximum rechargeMoney
	if maximum.UnmarshalJSON([]byte(cfg.MaxCNY)) != nil || price.Total > maximum {
		rechargeError(w, errors.New("含手续费的报价超过设置的单次上限；未创建订单"))
		return
	}
	quote := rechargeQuote{randomID(), account, in.Product, goods.Name, goods.User.Name, price.Total.String(), price.Fee.String(), cfg.TargetBase + " · " + cfg.TargetEmail, time.Now().Add(5 * time.Minute).Unix()}
	c := rechargeContext{Config: cfg, UserID: profile.ID, Channel: channel, Total: price.Total, Visitor: randomID()[:16], Password: randomID()}
	raw, err := c.marshal()
	if err != nil {
		rechargeError(w, errors.New("充值报价编码失败"))
		return
	}
	enc, err := s.control.encrypt(string(raw))
	if err != nil {
		rechargeError(w, errors.New("充值报价加密失败"))
		return
	}
	public, _ := json.Marshal(quote)
	_, err = s.control.db.ExecContext(ctx, `INSERT INTO console_recharge_quotes(id,owner,account_id,revision,public,encrypted_context,expires_at) VALUES($1,$2,$3,$4,$5,$6,to_timestamp($7))`, quote.ID, owner, account, revision, string(public), enc, quote.Expires)
	if err != nil {
		rechargeError(w, errors.New("充值报价保存失败"))
		return
	}
	writeJSON(w, 200, quote)
}
func (s *Service) rechargeJobs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	owner, err := s.controlUser(r)
	if err != nil {
		http.Error(w, "unauthorized", 401)
		return
	}
	account := r.PathValue("id")
	if _, _, _, err = s.rechargeAccount(r.Context(), owner, account); err != nil {
		rechargeError(w, err)
		return
	}
	if r.Method == "GET" {
		rows, err := s.control.db.QueryContext(r.Context(), `SELECT id,state,message,public,encrypted_context,extract(epoch FROM created_at)::bigint,extract(epoch FROM updated_at)::bigint FROM console_recharge_jobs WHERE account_id=$1 AND owner=$2 ORDER BY created_at DESC LIMIT 20`, account, owner)
		if err != nil {
			rechargeError(w, errors.New("充值记录读取失败"))
			return
		}
		defer rows.Close()
		jobs := []rechargeJob{}
		for rows.Next() {
			var j rechargeJob
			var public []byte
			var enc string
			if err := rows.Scan(&j.ID, &j.State, &j.Message, &public, &enc, &j.Created, &j.Updated); err != nil {
				rechargeError(w, errors.New("充值记录读取失败"))
				return
			}
			if json.Unmarshal(public, &j.Quote) != nil {
				rechargeError(w, errors.New("充值记录格式无效"))
				return
			}
			if raw, e := s.control.decrypt(enc); e == nil {
				if c, e := decodeRechargeContext(raw); e == nil {
					if rechargeOfficialAction(c.ActionURL) {
						j.ActionURL = c.ActionURL
					}
					j.Credit = c.Credit
					j.MerchantOrder = c.Trade
				}
			}
			jobs = append(jobs, j)
		}
		if rows.Err() != nil {
			rechargeError(w, errors.New("充值记录读取失败"))
			return
		}
		writeJSON(w, 200, map[string]any{"jobs": jobs})
		return
	}
	var in struct {
		Quote     string `json:"quote_id"`
		Confirmed bool   `json:"confirmed"`
		Total     string `json:"total_cny"`
		Key       string `json:"idempotency_key"`
	}
	if !decodeControl(w, r, &in) {
		return
	}
	if !in.Confirmed || !rechargeKey.MatchString(in.Key) {
		rechargeError(w, errors.New("请确认本次商品、目标账号与含手续费金额"))
		return
	}
	tx, err := s.control.db.BeginTx(r.Context(), nil)
	if err != nil {
		rechargeError(w, errors.New("充值任务创建失败"))
		return
	}
	defer tx.Rollback()
	var existing, existingQuote, existingAccount, existingTotal string
	err = tx.QueryRowContext(r.Context(), `SELECT id,quote_id,account_id,public->>'total_cny' FROM console_recharge_jobs WHERE owner=$1 AND (idempotency_key=$2 OR quote_id=$3) LIMIT 1`, owner, in.Key, in.Quote).Scan(&existing, &existingQuote, &existingAccount, &existingTotal)
	if err == nil {
		if existingQuote != in.Quote || existingAccount != account || existingTotal != in.Total {
			rechargeError(w, errors.New("幂等标识已用于其他报价"))
			return
		}
		writeJSON(w, 200, map[string]string{"id": existing})
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		rechargeError(w, errors.New("充值记录核对失败"))
		return
	}
	var public []byte
	var enc string
	err = tx.QueryRowContext(r.Context(), `SELECT q.public,q.encrypted_context FROM console_recharge_quotes q JOIN console_recharge_settings c ON c.account_id=q.account_id AND c.revision=q.revision WHERE q.id=$1 AND q.owner=$2 AND q.account_id=$3 AND q.expires_at>now() AND (c.config->>'enabled')::boolean FOR UPDATE OF q`, in.Quote, owner, account).Scan(&public, &enc)
	if err != nil {
		rechargeError(w, errors.New("报价已过期或充值设置已变化，请重新获取报价"))
		return
	}
	var quote rechargeQuote
	if json.Unmarshal(public, &quote) != nil || quote.Total != in.Total {
		rechargeError(w, errors.New("确认金额与本次报价不一致"))
		return
	}
	id := randomID()
	_, err = tx.ExecContext(r.Context(), `INSERT INTO console_recharge_jobs(id,owner,account_id,quote_id,idempotency_key,state,public,encrypted_context,message) VALUES($1,$2,$3,$4,$5,'queued',$6,$7,'已确认，等待实时下单；不是兑换码池')`, id, owner, account, in.Quote, in.Key, string(public), enc)
	if err != nil {
		rechargeError(w, errors.New("该账号已有充值任务或本次已提交，请先刷新充值记录核对，勿重复付款"))
		return
	}
	_, err = tx.ExecContext(r.Context(), `INSERT INTO console_recharge_events(job_id,state,message) VALUES($1,'queued','用户确认本次含手续费金额和目标账号')`, id)
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		rechargeError(w, errors.New("充值提交结果未确认，请刷新充值记录，勿重复提交"))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"id": id})
}

func (s *Service) rechargeWalletLock(ctx context.Context, owner string) (func(), error) {
	token := randomID()
	var result string
	err := s.control.db.QueryRowContext(ctx, `INSERT INTO console_recharge_wallet_locks(owner,token,expires_at) VALUES($1,$2,now()+interval '120 seconds') ON CONFLICT(owner) DO UPDATE SET token=excluded.token,expires_at=excluded.expires_at WHERE console_recharge_wallet_locks.expires_at<now() RETURNING token`, owner, token).Scan(&result)
	if err != nil {
		return nil, errors.New("当前钱包有操作进行中，请稍后查询，勿重复付款")
	}
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = s.control.db.ExecContext(ctx, `DELETE FROM console_recharge_wallet_locks WHERE owner=$1 AND token=$2`, owner, token)
	}, nil
}

func rechargeIntent(q rechargeQuote) string {
	// Product names are untrusted web data, not prompt/command instructions.
	return fmt.Sprintf("服务内容：站点额度兑换码（1件），支付金额：¥%s，支付对象：链动店铺", q.Total)
}
