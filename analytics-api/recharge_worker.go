package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"math"
	"net/http"
	"strconv"
	"time"
)

func (s *Service) rechargeLoop(ctx context.Context) {
	for ctx.Err() == nil {
		if !s.rechargeWorkOne(ctx) && !waitStartup(ctx, 3*time.Second) {
			return
		}
	}
}
func (s *Service) rechargeWorkOne(parent context.Context) bool {
	ctx, cancel := context.WithTimeout(parent, 80*time.Second)
	defer cancel()
	j := rechargeJob{lease: randomID()}
	var raw []byte
	var encrypted string
	err := s.control.db.QueryRowContext(ctx, `UPDATE console_recharge_jobs SET lease_token=$1,lease_until=now()+interval '120 seconds' WHERE id=(SELECT id FROM console_recharge_jobs WHERE state NOT IN ('done','failed','closed','review') AND next_attempt<=now() AND (lease_until IS NULL OR lease_until<now()) ORDER BY next_attempt FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING id,owner,account_id,state,public,encrypted_context,extract(epoch FROM created_at)::bigint`, j.lease).Scan(&j.ID, &j.owner, &j.account, &j.State, &raw, &encrypted, &j.Created)
	if err != nil {
		return false
	}
	defer func() {
		c, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		_, _ = s.control.db.ExecContext(c, `UPDATE console_recharge_jobs SET lease_token='',lease_until=NULL,next_attempt=greatest(next_attempt,now()+interval '15 seconds') WHERE id=$1 AND lease_token=$2`, j.ID, j.lease)
	}()
	plain, err := s.control.decrypt(encrypted)
	if err == nil {
		j.context, err = decodeRechargeContext(plain)
	}
	if err == nil {
		err = json.Unmarshal(raw, &j.Quote)
	}
	if err != nil {
		// A missing/rotated master key must not destroy the original encrypted
		// order. Preserve it so restoring the correct key permits recovery.
		_ = s.rechargeSaveEncrypted(ctx, &j, "review", "充值记录无法解密，请管理员核对原订单，勿重新付款", encrypted)
		return true
	}
	unlock, err := s.rechargeWalletLock(ctx, j.owner)
	if err != nil {
		return false
	}
	defer unlock()
	switch j.State {
	case "queued":
		s.rechargeCreate(ctx, &j)
	case "creating":
		// A process may die after the shop accepted an order but before its ID
		// was saved. This upstream has no documented idempotency key. NEVER retry.
		_ = s.rechargeSave(ctx, &j, "review", "下单结果未确认；请核对链动订单，系统不会重复下单或付款")
	case "paying":
		_ = s.rechargeSave(ctx, &j, "awaiting_payment", "付款请求已发送或结果未知，正在核对原订单；不会再次发起付款")
	case "awaiting_payment":
		s.rechargeDelivery(ctx, &j)
	case "redeeming":
		_ = s.rechargeSave(ctx, &j, "verifying", "兑换结果尚未确认，只读取当前账号兑换历史，不重复提交兑换")
	case "verifying":
		s.rechargeVerify(ctx, &j)
	default:
		_ = s.rechargeSave(ctx, &j, "review", "充值状态不兼容，请核对原订单")
	}
	return true
}

func (s *Service) rechargeSave(ctx context.Context, j *rechargeJob, state, message string) error {
	raw, err := j.context.marshal()
	if err != nil {
		return err
	}
	enc, err := s.control.encrypt(string(raw))
	if err != nil {
		return err
	}
	return s.rechargeSaveEncrypted(ctx, j, state, message, enc)
}

func (s *Service) rechargeSaveEncrypted(ctx context.Context, j *rechargeJob, state, message, enc string) error {
	tx, err := s.control.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE console_recharge_jobs SET state=$3,message=$4,encrypted_context=$5,updated_at=now(),next_attempt=now()+interval '15 seconds' WHERE id=$1 AND lease_token=$2 AND lease_until>now()`, j.ID, j.lease, state, message, enc)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return errors.New("recharge lease lost")
	}
	if j.State != state || j.Message != message {
		if _, err = tx.ExecContext(ctx, `INSERT INTO console_recharge_events(job_id,state,message) VALUES($1,$2,$3)`, j.ID, state, message); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err == nil {
		j.State, j.Message = state, message
		// Only opaque task ID and the normalized state; no account, card, CLI
		// output, authorization link or merchant response is logged.
		log.Printf("recharge_transition job=%s state=%s", j.ID, state)
	}
	return err
}

func (s *Service) rechargeCreate(ctx context.Context, j *rechargeJob) {
	fail := func(message string) { _ = s.rechargeSave(ctx, j, "failed", message+"；未创建订单或付款") }
	var current bool
	err := s.control.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM console_recharge_quotes q JOIN console_recharge_settings c ON c.account_id=q.account_id AND c.revision=q.revision WHERE q.id=$1 AND q.expires_at>now() AND (c.config->>'enabled')::boolean)`, j.Quote.ID).Scan(&current)
	if err != nil || !current {
		fail("报价过期或设置变化")
		return
	}
	if _, _, err := s.rechargeIdentity(ctx, j.owner, j.account, j.context.Config, j.context.UserID); err != nil {
		fail(err.Error())
		return
	}
	if !s.rechargePayments().Configured(j.owner) {
		fail("支付宝支付代理未配置")
		return
	}
	wallet, err := s.rechargePayments().Wallet(ctx, j.owner, "status", "")
	if err != nil || wallet.Status != "bound" {
		fail("支付宝钱包未就绪")
		return
	}
	shop, _, err := parseRechargeShop(j.context.Config.ShopURL)
	if err != nil {
		fail("店铺配置无效")
		return
	}
	if _, err = shop.goods(ctx, subHTTP, j.Quote.Product); err != nil {
		fail(err.Error())
		return
	}
	channel, err := shop.channel(ctx, subHTTP)
	if err != nil || channel != j.context.Channel {
		fail("支付渠道已变化")
		return
	}
	price, err := shop.price(ctx, subHTTP, j.Quote.Product, j.context.Channel)
	if err != nil || price.Total != j.context.Total {
		fail("实时价格已变化，请重新核对报价")
		return
	}
	buyer, err := shop.buyer(ctx, subHTTP)
	if err != nil {
		fail(err.Error())
		return
	}
	if err = s.rechargeSave(ctx, j, "creating", "正在创建一笔实时订单；中断后不会自动重建"); err != nil {
		return
	}
	var order rechargeOrder
	err = shop.request(ctx, subHTTP, "/shopApi/Pay/order", map[string]any{"goods_key": j.Quote.Product, "quantity": 1, "coupon_code": "", "channel_id": j.context.Channel, "contact": j.context.Config.Contact, "query_password": j.context.Password, "select_cards_ids": []string{}, "extend": map[string]string{"juuid": buyer}}, &order, j.context.Visitor)
	if rechargeOrderID.MatchString(order.Trade) {
		j.context.Trade = order.Trade
	}
	if err != nil || j.context.Trade == "" || order.Total != j.context.Total || (!rechargeMerchantURL(order.PayURL) && !rechargeAlipayURL(order.PayURL)) {
		_ = s.rechargeSave(ctx, j, "review", "下单返回未完整核验；不会自动重试下单或付款，请核对链动原订单")
		return
	}
	j.context.PaymentURL = order.PayURL
	if err = s.rechargeSave(ctx, j, "awaiting_payment", "订单已创建，准备交给支付宝官方支付能力"); err != nil {
		return
	}
	cashier, err := rechargeCashier(ctx, subHTTP, order.PayURL)
	if err != nil {
		_ = s.rechargeSave(ctx, j, "awaiting_payment", err.Error()+"；可打开原收银台核对，系统不另建订单")
		return
	}
	// Persist BEFORE invoking a non-idempotent payment operation. A crash here
	// sacrifices automatic retry rather than risking a second charge.
	if err = s.rechargeSave(ctx, j, "paying", "正在提交一次支付宝付款；中断后仅查询原订单"); err != nil {
		return
	}
	link, err := s.rechargePayments().Submit(ctx, j.owner, cashier, rechargeIntent(j.Quote))
	if rechargeOfficialAction(link) {
		j.context.ActionURL = link
	}
	message := "付款已交给支付宝，等待授权或商户到账；受理不等于支付成功"
	if err != nil {
		message = "付款结果未知，系统仅核对原订单，不会自动再次付款"
	}
	_ = s.rechargeSave(ctx, j, "awaiting_payment", message)
}

func (s *Service) rechargeDelivery(ctx context.Context, j *rechargeJob) {
	if j.context.Trade == "" {
		_ = s.rechargeSave(ctx, j, "review", "缺少可核对的原订单编号，请人工核对，勿重新付款")
		return
	}
	shop, _, err := parseRechargeShop(j.context.Config.ShopURL)
	if err != nil {
		return
	}
	var order rechargeOrder
	err = shop.request(ctx, subHTTP, "/shopApi/Order/info", map[string]any{"trade_no": j.context.Trade, "query_password": j.context.Password, "dump": 1}, &order, j.context.Visitor)
	if err != nil {
		if time.Now().Unix()-j.Created > 86400 {
			_ = s.rechargeSave(ctx, j, "review", "超过一天仍未确认原订单，请人工核对，不会重新下单或付款")
		}
		return
	}
	// Never trust a callback, client success hint, balance delta or CLI prose.
	if order.Trade != j.context.Trade || order.Total != j.context.Total || order.Goods.Key != j.Quote.Product || order.Quantity != 1 || order.Goods.Type != "card" {
		_ = s.rechargeSave(ctx, j, "review", "原订单的商品、数量或金额不一致；已阻止自动兑换")
		return
	}
	if order.Status != 1 {
		if order.Status != 0 || time.Now().Unix()-j.Created > 86400 {
			_ = s.rechargeSave(ctx, j, "review", "商户未确认已付款或订单状态不兼容，请核对原订单；不重复付款")
		}
		return
	}
	if len(order.Response.Cards) != 1 {
		_ = s.rechargeSave(ctx, j, "review", "商户确认已付款，但未交付唯一兑换码；请核对发货，不会重复付款")
		return
	}
	code := order.Response.Cards[0]
	if !rechargeCode.MatchString(code) {
		_ = s.rechargeSave(ctx, j, "review", "发货内容不是已适配的纯兑换码，已阻止自动提交；请核对原订单")
		return
	}
	j.context.Code = code
	token, _, err := s.rechargeIdentity(ctx, j.owner, j.account, j.context.Config, j.context.UserID)
	if err != nil {
		_ = s.rechargeSave(ctx, j, "review", err.Error())
		return
	}
	if err = s.rechargeSave(ctx, j, "redeeming", "已核验付款和唯一兑换码，正在向绑定账号提交一次兑换"); err != nil {
		return
	}
	// This upstream does not document idempotent redeem writes. Do not attach a
	// made-up key or retry on timeout/401; verify the exact code in history.
	var result rechargeRedemption
	err = subJSON(ctx, subHTTP, j.context.Config.TargetBase, http.MethodPost, "/api/v1/redeem", token, map[string]string{"code": code}, &result, "")
	message := "兑换已提交，正在核对当前账号的准确兑换记录"
	if err != nil {
		message = "兑换响应未确认，正在只读核对兑换历史；不会重复提交兑换或付款"
	}
	_ = s.rechargeSave(ctx, j, "verifying", message)
}

type rechargeRedemption struct {
	Code   string  `json:"code"`
	Type   string  `json:"type"`
	Status string  `json:"status"`
	Value  float64 `json:"value"`
	UsedBy *int64  `json:"used_by"`
}

// Older sub2api releases ignore pagination and return the latest 25 items as
// an array. Accept that shape without inventing extra pages or balance proof.
type rechargeHistory struct {
	Items  []rechargeRedemption `json:"items"`
	Total  int                  `json:"total"`
	legacy bool
}

func (h *rechargeHistory) UnmarshalJSON(raw []byte) error {
	if len(raw) > 0 && raw[0] == '[' {
		h.legacy = true
		if err := json.Unmarshal(raw, &h.Items); err != nil {
			return err
		}
		h.Total = len(h.Items)
		return nil
	}
	type wire rechargeHistory
	return json.Unmarshal(raw, (*wire)(h))
}

func (s *Service) rechargeVerify(ctx context.Context, j *rechargeJob) {
	if !rechargeCode.MatchString(j.context.Code) {
		_ = s.rechargeSave(ctx, j, "review", "缺少可核对的准确兑换码，不会重复提交兑换")
		return
	}
	token, _, err := s.rechargeIdentity(ctx, j.owner, j.account, j.context.Config, j.context.UserID)
	if err != nil {
		return
	}
	for page := 1; page <= 5; page++ {
		var history rechargeHistory
		err = subJSON(ctx, subHTTP, j.context.Config.TargetBase, "GET", "/api/v1/redeem/history?page="+strconv.Itoa(page)+"&page_size=100", token, nil, &history, "")
		if err != nil {
			break
		}
		for _, code := range history.Items {
			if code.Code != j.context.Code {
				continue
			}
			if code.UsedBy == nil || *code.UsedBy != j.context.UserID || code.Type != "balance" || code.Status != "used" || code.Value <= 0 || math.IsNaN(code.Value) || math.IsInf(code.Value, 0) {
				_ = s.rechargeSave(ctx, j, "review", "准确兑换记录的账号、类型或额度异常，请核对；不会重复付款")
				return
			}
			j.context.Credit = &code.Value
			if s.rechargeSave(ctx, j, "done", "准确兑换码已在绑定账号的历史中确认到账") == nil {
				_, _ = s.control.db.ExecContext(ctx, `UPDATE console_sub_accounts SET balance=NULL WHERE id=$1 AND owner=$2 AND base=$3 AND lower(email)=lower($4)`, j.account, j.owner, j.context.Config.TargetBase, j.context.Config.TargetEmail)
			}
			return
		}
		if history.legacy || len(history.Items) < 100 || page*100 >= history.Total {
			break
		}
	}
	if time.Now().Unix()-j.Created > 3600 {
		_ = s.rechargeSave(ctx, j, "review", "一小时内未能在绑定账号确认该兑换码，请人工核对；不再次提交兑换或付款")
	}
}

// Reconciliation never repeats create/pay/redeem. If a paid order has not yet
// reached the redeem boundary, delivery may submit its FIRST redemption.
func (s *Service) rechargeReconcile(w http.ResponseWriter, r *http.Request) {
	owner, err := s.controlUser(r)
	if err != nil {
		http.Error(w, "unauthorized", 401)
		return
	}
	var enc, state string
	err = s.control.db.QueryRowContext(r.Context(), `SELECT encrypted_context,state FROM console_recharge_jobs WHERE id=$1 AND account_id=$2 AND owner=$3`, r.PathValue("job"), r.PathValue("id"), owner).Scan(&enc, &state)
	if err != nil {
		rechargeError(w, err)
		return
	}
	plain, err := s.control.decrypt(enc)
	var c rechargeContext
	if err == nil {
		c, err = decodeRechargeContext(plain)
	}
	if err != nil {
		rechargeError(w, errors.New("充值记录无法解密"))
		return
	}
	if c.Code != "" {
		state = "verifying"
	} else if c.Trade != "" {
		state = "awaiting_payment"
	} else {
		rechargeError(w, errors.New("没有已保存的订单编号，只能在官方店铺人工核对；不会重新下单"))
		return
	}
	result, err := s.control.db.ExecContext(r.Context(), `UPDATE console_recharge_jobs SET state=$4,message='核对原订单并继续尚未提交的交付；不会重新下单、付款或重复兑换',next_attempt=now(),updated_at=now() WHERE id=$1 AND account_id=$2 AND owner=$3 AND state IN ('review','awaiting_payment','paying','redeeming','verifying') AND (lease_until IS NULL OR lease_until<now())`, r.PathValue("job"), r.PathValue("id"), owner, state)
	if err != nil {
		rechargeError(w, errors.New("原订单核对提交失败"))
		return
	}
	if n, _ := result.RowsAffected(); n != 1 {
		rechargeError(w, errors.New("充值任务正在处理、已结束或不存在，请先刷新状态"))
		return
	}
	writeJSON(w, 200, map[string]bool{"checking": true})
}

func (s *Service) rechargeRecovery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	owner, err := s.controlUser(r)
	if err != nil {
		http.Error(w, "unauthorized", 401)
		return
	}
	var enc string
	err = s.control.db.QueryRowContext(r.Context(), `SELECT encrypted_context FROM console_recharge_jobs WHERE id=$1 AND account_id=$2 AND owner=$3`, r.PathValue("job"), r.PathValue("id"), owner).Scan(&enc)
	if err != nil {
		rechargeError(w, err)
		return
	}
	plain, err := s.control.decrypt(enc)
	var c rechargeContext
	if err == nil {
		c, err = decodeRechargeContext(plain)
	}
	if err != nil {
		rechargeError(w, errors.New("充值记录无法解密"))
		return
	}
	payURL := ""
	if rechargeMerchantURL(c.PaymentURL) || rechargeAlipayURL(c.PaymentURL) {
		payURL = c.PaymentURL
	}
	writeJSON(w, 200, map[string]string{"merchant_order": c.Trade, "query_password": c.Password, "code": c.Code, "payment_url": payURL})
}
