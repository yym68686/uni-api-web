package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// The merchant adapter is deliberately not a general URL fetcher. Never send
// the sub2api JWT (including one pasted in an embedded-shop URL) to the shop.
var rechargeKey = regexp.MustCompile(`^[A-Za-z0-9_-]{3,80}$`)
var rechargeShopPath = regexp.MustCompile(`^/shop/([A-Za-z0-9_-]{3,80})/?$`)
var rechargeOrderID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,160}$`)
var rechargeCode = regexp.MustCompile(`^[A-Za-z0-9_-]{8,200}$`)

type rechargeShop struct{ Base, Token string }

func parseRechargeShop(raw string) (rechargeShop, string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.User != nil || (u.Host != "wzyp.cn" && u.Host != "pay.ldxp.cn") {
		return rechargeShop{}, "", errors.New("仅支持 wzyp.cn 或 pay.ldxp.cn 的 HTTPS 链动店铺链接")
	}
	m := rechargeShopPath.FindStringSubmatch(u.Path)
	if len(m) != 2 {
		return rechargeShop{}, "", errors.New("请输入 /shop/店铺编号 链接")
	}
	s := rechargeShop{Base: "https://" + u.Host, Token: m[1]}
	return s, s.Base + "/shop/" + s.Token, nil // intentionally discard ALL query/fragment
}

type rechargeMoney int64

// Currency is integer fen throughout, including the confirmation/idempotency
// boundary. Reject excess precision rather than rounding an increased price.
func (m *rechargeMoney) UnmarshalJSON(raw []byte) error {
	s := string(raw)
	if strings.HasPrefix(s, `"`) {
		if err := json.Unmarshal(raw, &s); err != nil {
			return errors.New("invalid CNY amount")
		}
	}
	if !regexp.MustCompile(`^\d{1,8}(\.\d{1,2})?$`).MatchString(s) {
		return errors.New("invalid CNY amount")
	}
	p := strings.Split(s, ".")
	n, _ := strconv.ParseInt(p[0], 10, 64)
	if len(p) == 2 {
		f, _ := strconv.ParseInt((p[1] + "00")[:2], 10, 64)
		n = n*100 + f
	} else {
		n *= 100
	}
	*m = rechargeMoney(n)
	return nil
}
func (m rechargeMoney) String() string { return fmt.Sprintf("%d.%02d", m/100, m%100) }

type rechargeGoods struct {
	Key    string        `json:"goods_key"`
	Name   string        `json:"name"`
	Type   string        `json:"goods_type"`
	Status int           `json:"status"`
	Price  rechargeMoney `json:"price"`
	User   struct {
		Token string `json:"token"`
		Name  string `json:"nickname"`
	} `json:"user"`
	Extend struct {
		Minimum  int `json:"limit_count"`
		Password int `json:"query_password_status"`
	} `json:"extend"`
}
type rechargeProduct struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	Price string `json:"price_cny"`
}
type rechargePrice struct {
	Total rechargeMoney `json:"total_amount"`
	Fee   rechargeMoney `json:"fee"`
}

func (s rechargeShop) request(ctx context.Context, client *http.Client, path string, input, out any, visitor string) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", s.Base+path, bytes.NewReader(raw))
	if err != nil {
		return errors.New("充值店铺地址无效")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Origin", s.Base)
	req.Header.Set("Referer", s.Base+"/shop/"+s.Token)
	if visitor != "" {
		req.Header.Set("Visitorid", visitor)
	}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("链动接口连接中断；写入结果可能未知")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil || len(body) > 2<<20 {
		return errors.New("链动接口读取失败")
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("链动接口 HTTP %d（可能需要验证或稍后再试）", resp.StatusCode)
	}
	var envelope struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Code != 1 || len(envelope.Data) == 0 {
		return errors.New("链动未返回可验证的结果；请在官方店铺核对")
	}
	if out != nil && json.Unmarshal(envelope.Data, out) != nil {
		return errors.New("链动接口格式不兼容")
	}
	return nil
}
func (s rechargeShop) catalog(ctx context.Context, client *http.Client) ([]rechargeProduct, error) {
	products := []rechargeProduct{}
	seen := map[string]bool{}
	for page := 1; page <= 10; page++ {
		var data struct {
			Total int             `json:"total"`
			List  []rechargeGoods `json:"list"`
		}
		err := s.request(ctx, client, "/shopApi/Shop/goodsList", map[string]any{"token": s.Token, "goods_type": "card", "current": page, "pageSize": 50, "keywords": "", "category_id": ""}, &data, "")
		if err != nil {
			return nil, err
		}
		for _, g := range data.List {
			if g.Type != "card" || g.User.Token != s.Token || !rechargeKey.MatchString(g.Key) || seen[g.Key] {
				continue
			}
			seen[g.Key] = true
			products = append(products, rechargeProduct{g.Key, g.Name, g.Price.String()})
		}
		if len(data.List) < 50 || page*50 >= data.Total {
			return products, nil
		}
	}
	return nil, errors.New("店铺商品过多，请使用专用充值店铺")
}
func (s rechargeShop) goods(ctx context.Context, client *http.Client, key string) (rechargeGoods, error) {
	var g rechargeGoods
	if !rechargeKey.MatchString(key) {
		return g, errors.New("充值商品编号无效")
	}
	err := s.request(ctx, client, "/shopApi/Shop/goodsInfo", map[string]string{"goods_key": key, "trade_no": ""}, &g, "")
	if err == nil && (g.Key != key || g.Type != "card" || g.User.Token != s.Token || g.Status != 1 || g.Extend.Minimum > 1) {
		err = errors.New("商品已下架、不是单件卡密或不属于当前店铺")
	}
	return g, err
}
func (s rechargeShop) price(ctx context.Context, client *http.Client, key string, channel int) (rechargePrice, error) {
	var p rechargePrice
	err := s.request(ctx, client, "/shopApi/Shop/getGoodsPrice", map[string]any{"goods_key": key, "quantity": 1, "coupon_code": "", "channel_id": channel}, &p, "")
	if err == nil && (p.Total <= 0 || p.Fee < 0 || p.Fee > p.Total) {
		err = errors.New("充值报价无效")
	}
	return p, err
}
func (s rechargeShop) channel(ctx context.Context, client *http.Client) (int, error) {
	var channels []struct {
		ID     int    `json:"id"`
		Code   string `json:"code"`
		Status int    `json:"status"`
	}
	if err := s.request(ctx, client, "/shopApi/Shop/getUserChannel", map[string]string{"token": s.Token}, &channels, ""); err != nil {
		return 0, err
	}
	for _, c := range channels {
		if c.Status == 1 && c.Code == "AlipayPc" && c.ID > 0 {
			return c.ID, nil
		}
	}
	return 0, errors.New("店铺暂未提供已适配的支付宝电脑收款方式")
}

// Read the first-party buyer identity; never execute merchant JavaScript or
// follow an arbitrary iframe URL with site credentials.
func (s rechargeShop) buyer(ctx context.Context, client *http.Client) (string, error) {
	body, _, err := rechargeGET(ctx, client, s.Base+"/shopApi/common/buyerBlackIframe")
	if err != nil {
		return "", err
	}
	m := regexp.MustCompile(`(?:const\s+juuid|juuid)\s*=\s*["']([A-Za-z0-9_-]{1,32})["']`).FindSubmatch(body)
	if len(m) != 2 {
		return "", errors.New("店铺买家身份格式不兼容；未创建订单")
	}
	return string(m[1]), nil
}

type rechargeOrder struct {
	Trade    string        `json:"trade_no"`
	Total    rechargeMoney `json:"total_amount"`
	PayURL   string        `json:"payurl"`
	Status   int           `json:"status"`
	Quantity int           `json:"quantity"`
	Goods    struct {
		Key  string `json:"goods_key"`
		Type string `json:"goods_type"`
	} `json:"goods"`
	Response struct {
		Cards []string `json:"cards"`
	} `json:"response"`
}

func rechargeMerchantURL(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Scheme == "https" && u.User == nil && u.Fragment == "" && (u.Host == "wzyp.cn" || u.Host == "pay.ldxp.cn")
}
func rechargeAlipayURL(raw string) bool {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Fragment != "" {
		return false
	}
	h := u.Hostname()
	return h == "qr.alipay.com" || h == "excashier.alipay.com" || h == "cashier.alipay.com" || regexp.MustCompile(`^cashier[a-z0-9-]*\.alipay\.com$`).MatchString(h)
}
func rechargeGET(ctx context.Context, client *http.Client, raw string) ([]byte, string, error) {
	if !rechargeMerchantURL(raw) {
		return nil, "", errors.New("拒绝未知充值跳转地址")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", errors.New("收银台读取失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		u, err := resp.Location()
		if err != nil {
			return nil, "", errors.New("收银台跳转无效")
		}
		return nil, u.String(), nil
	}
	if resp.StatusCode != 200 {
		return nil, "", errors.New("收银台需要人工验证")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		return nil, "", errors.New("收银台内容不兼容")
	}
	return body, "", nil
}
func rechargeCashier(ctx context.Context, client *http.Client, raw string) (string, error) {
	for i := 0; i < 4; i++ {
		if rechargeAlipayURL(raw) {
			return raw, nil
		}
		body, next, err := rechargeGET(ctx, client, raw)
		if err != nil {
			return "", err
		}
		if next != "" {
			raw = next
			continue
		}
		// Read-only extraction only. Multiple distinct links are ambiguous.
		text := html.UnescapeString(strings.ReplaceAll(string(body), `\/`, `/`))
		links := regexp.MustCompile(`https://[^\s"'<>\\]+`).FindAllString(text, -1)
		found := map[string]bool{}
		for _, link := range links {
			if rechargeAlipayURL(link) {
				found[link] = true
			}
		}
		if len(found) != 1 {
			return "", errors.New("未能取得唯一的支付宝官方收银台；不会自动付款，请核对订单")
		}
		for link := range found {
			return link, nil
		}
	}
	return "", errors.New("收银台跳转过多；不会自动付款")
}
