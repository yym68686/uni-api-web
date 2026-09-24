package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type newAPILog struct {
	ID       int64       `json:"id"`
	Type     int         `json:"type"`
	Key      int64       `json:"token_id"`
	Group    string      `json:"group"`
	Request  string      `json:"request_id"`
	Model    string      `json:"model_name"`
	At       int64       `json:"created_at"`
	Quota    json.Number `json:"quota"`
	Input    int64       `json:"prompt_tokens"`
	Output   int64       `json:"completion_tokens"`
	Duration int64       `json:"use_time"`
	Stream   bool        `json:"is_stream"`
	Other    string      `json:"other"`
}

func newAPICost(quota json.Number, unit float64) (json.Number, error) {
	q, ok := new(big.Rat).SetString(quota.String())
	if !ok || q.Sign() < 0 {
		return "", errors.New("账单额度无效")
	}
	u, ok := new(big.Rat).SetString(strconv.FormatFloat(unit, 'f', -1, 64))
	if !ok || u.Sign() <= 0 {
		return "", errors.New("额度单位无效")
	}
	return json.Number(new(big.Rat).Quo(q, u).FloatString(10)), nil
}

// Only a small, explicitly supported linear grammar is interpreted. This is
// not an expression evaluator: request/time/image/audio rules remain unknown.
var newAPILinear = regexp.MustCompile(`^(p|c|cr|cc|cc1h)\s*\*\s*([0-9]+(?:\.[0-9]+)?)$`)
var newAPITier = regexp.MustCompile(`^tier\("([^"\r\n]+)",\s*([^()]+)\)$`)
var newAPITwoTier = regexp.MustCompile(`^len\s*<=\s*[0-9]+\s*\?\s*(tier\("[^"\r\n]+",\s*[^()]+\))\s*:\s*(tier\("[^"\r\n]+",\s*[^()]+\))$`)
var newAPIServiceRule = regexp.MustCompile(`^\s*\*\s*\((param\("service_tier"\)\s*==\s*"(?:flex|priority|default)")\s*\?\s*([0-9]+(?:\.[0-9]+)?)\s*:\s*1\)`)

func newAPIReceiptPrices(meta map[string]json.RawMessage) (map[string]float64, bool) {
	var encoded, tier string
	_ = json.Unmarshal(meta["expr_b64"], &encoded)
	_ = json.Unmarshal(meta["matched_tier"], &tier)
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(raw) > 8192 {
		return nil, false
	}
	expr := strings.TrimSpace(strings.TrimPrefix(string(raw), "v1:"))
	var rules []struct {
		Cond       string  `json:"cond"`
		Matched    *bool   `json:"matched"`
		Multiplier float64 `json:"multiplier"`
	}
	if value := meta["request_rules"]; len(value) > 0 && json.Unmarshal(value, &rules) != nil {
		return nil, false
	}
	base, rest := expr, ""
	if strings.HasPrefix(expr, "(") {
		depth := 0
		quoted := false
		for i, c := range expr {
			if c == '"' {
				quoted = !quoted
			}
			if quoted {
				continue
			}
			if c == '(' {
				depth++
			}
			if c == ')' {
				depth--
				if depth == 0 {
					base, rest = expr[1:i], expr[i+1:]
					break
				}
			}
		}
	}
	prices, ok := newAPIExpressionPrices(base64.StdEncoding.EncodeToString([]byte(base)), tier)
	if !ok {
		return nil, false
	}
	factor := 1.0
	index := 0
	for strings.TrimSpace(rest) != "" {
		match := newAPIServiceRule.FindStringSubmatch(rest)
		if match == nil || index >= len(rules) || rules[index].Matched == nil {
			return nil, false
		}
		multiplier, e := strconv.ParseFloat(match[2], 64)
		if e != nil || math.IsInf(multiplier, 0) || rules[index].Multiplier != multiplier || strings.Join(strings.Fields(rules[index].Cond), "") != strings.Join(strings.Fields(match[1]), "") {
			return nil, false
		}
		if *rules[index].Matched {
			factor *= multiplier
		}
		index++
		rest = rest[len(match[0]):]
	}
	if index != len(rules) {
		return nil, false
	}
	for name, price := range prices {
		prices[name] = price * factor
	}
	return prices, true
}

func newAPIExpressionPrices(encoded, tier string) (map[string]float64, bool) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(raw) > 8192 {
		return nil, false
	}
	expr := strings.TrimSpace(strings.TrimPrefix(string(raw), "v1:"))
	parts := []string{expr}
	if pair := newAPITwoTier.FindStringSubmatch(expr); pair != nil {
		parts = pair[1:]
	}
	selected := ""
	matches := 0
	for _, part := range parts {
		m := newAPITier.FindStringSubmatch(strings.TrimSpace(part))
		if m == nil {
			return nil, false
		}
		if m[1] == tier {
			selected = m[2]
			matches++
		}
	}
	if matches != 1 {
		return nil, false
	}
	prices := map[string]float64{}
	for _, term := range strings.Split(selected, "+") {
		m := newAPILinear.FindStringSubmatch(strings.TrimSpace(term))
		if m == nil {
			return nil, false
		}
		if _, ok := prices[m[1]]; ok {
			return nil, false
		}
		v, e := strconv.ParseFloat(m[2], 64)
		if e != nil || math.IsInf(v, 0) {
			return nil, false
		}
		prices[m[1]] = v
	}
	_, p := prices["p"]
	_, c := prices["c"]
	return prices, p && c
}
func newAPIReceipt(log newAPILog, unit float64) (subUsageLog, json.Number, error) {
	cost, err := newAPICost(log.Quota, unit)
	if err != nil {
		return subUsageLog{}, "", err
	}
	amount, _ := cost.Float64()
	input, output := log.Input, log.Output
	duration := log.Duration * 1000
	out := subUsageLog{KeyID: log.Key, Model: log.Model, RequestID: "newapi:" + log.Request, CreatedAt: time.Unix(log.At, 0).UTC().Format(time.RFC3339), ActualCost: &amount, InputTokens: &input, OutputTokens: &output, DurationMS: &duration, BillingMode: "newapi_unconfirmed"}
	var meta map[string]json.RawMessage
	if json.Unmarshal([]byte(log.Other), &meta) != nil {
		return out, cost, nil
	}
	str := func(key string) string { var v string; _ = json.Unmarshal(meta[key], &v); return v }
	num := func(key string) *float64 {
		var v float64
		if json.Unmarshal(meta[key], &v) != nil || subValidCost(&v) == nil {
			return nil
		}
		return &v
	}
	if source := str("billing_source"); source != "" && source != "wallet" {
		out.ActualCost = nil
		return out, "", errors.New("账单使用订阅或其他额度，无法确认为钱包消费")
	}
	rate := num("group_ratio")
	out.Rate = rate
	if v := num("frt"); v != nil {
		ms := int64(*v)
		out.FirstTokenMS = &ms
	}
	mode := str("billing_mode")
	prices := map[string]float64{}
	counts := map[string]int64{"p": input, "c": output}
	switch mode {
	case "tiered_expr":
		var ok bool
		prices, ok = newAPIReceiptPrices(meta)
		if !ok {
			return out, cost, nil
		}
		var tokens map[string]float64
		if json.Unmarshal(meta["billing_tokens"], &tokens) == nil && tokens != nil {
			for name := range prices {
				v, exists := tokens[name]
				if !exists || v < 0 || v != math.Trunc(v) || v > float64(1<<53) {
					return out, cost, nil
				}
				counts[name] = int64(v)
			}
		} else {
			// Older receipts omit normalized tokens. Only infer plain text counts
			// when every cache component is explicitly absent/zero; verify the charge.
			for _, k := range []string{"cache_tokens", "image_tokens", "audio_tokens"} {
				if v := num(k); v != nil && *v != 0 {
					return out, cost, nil
				}
			}
			if _, usesWrite := prices["cc"]; usesWrite {
				for _, k := range []string{"cache_creation_tokens", "cache_creation_5m_tokens", "cache_creation_1h_tokens"} {
					if v := num(k); v != nil && *v != 0 {
						return out, cost, nil
					}
				}
			}
			for name := range prices {
				if name != "p" && name != "c" {
					counts[name] = 0
				}
			}
		}
	case "", "ratio":
		if price := num("model_price"); price != nil && *price > 0 {
			return out, cost, nil
		}
		ratio, completion := num("model_ratio"), num("completion_ratio")
		if ratio == nil || completion == nil {
			return out, cost, nil
		}
		for _, k := range []string{"cache_tokens", "cache_creation_tokens", "image_tokens", "audio_tokens"} {
			if v := num(k); v != nil && *v != 0 {
				return out, cost, nil
			}
		}
		prices["p"] = *ratio * 1e6 / unit
		prices["c"] = prices["p"] * *completion
	default:
		return out, cost, nil
	}
	if rate == nil {
		return out, cost, nil
	}
	total := 0.0
	for name, price := range prices {
		total += float64(counts[name]) * price / 1e6
	}
	if math.Abs(total**rate-amount) > 1.01/unit {
		return out, cost, nil
	}
	out.BillingMode = "token"
	out.TotalCost = &total
	inCount, outCount := counts["p"], counts["c"]
	out.InputTokens = &inCount
	out.OutputTokens = &outCount
	inCost, outCost := float64(inCount)*prices["p"]/1e6, float64(outCount)*prices["c"]/1e6
	out.InputCost = &inCost
	out.OutputCost = &outCost
	if p, ok := prices["cr"]; ok {
		count := counts["cr"]
		value := float64(count) * p / 1e6
		out.CacheReadTokens = &count
		out.CacheReadCost = &value
	}
	if p, ok := prices["cc"]; ok && counts["cc1h"] == 0 {
		count := counts["cc"]
		value := float64(count) * p / 1e6
		out.CacheWriteTokens = &count
		out.CacheWriteCost = &value
	}
	return out, cost, nil
}
func (s *Service) newAPIUsage(ctx context.Context, account, base string, q url.Values, out any) error {
	auth, _, err := s.newAPIAuth(ctx, account)
	if err != nil {
		return err
	}
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	params := url.Values{"p": {strconv.Itoa(page)}, "page_size": {"100"}, "type": {"2"}}
	for _, field := range []string{"start_date", "end_date"} {
		if q.Get(field) != "" {
			at, e := time.Parse("2006-01-02", q.Get(field))
			if e != nil {
				return e
			}
			if field == "start_date" {
				params.Set("start_timestamp", strconv.FormatInt(at.Unix(), 10))
			} else {
				params.Set("end_timestamp", strconv.FormatInt(at.Add(24*time.Hour-time.Second).Unix(), 10))
			}
		}
	}
	for _, field := range []string{"start_timestamp", "end_timestamp"} {
		if value := q.Get(field); value != "" {
			if _, e := strconv.ParseInt(value, 10, 64); e != nil {
				return e
			}
			params.Set(field, value)
		}
	}
	// Preserve the full remote page for account scans. Probe lookups filter by
	// token name at the server and still validate its immutable token_id locally.
	if key := q.Get("api_key_id"); key != "" {
		var token newAPIToken
		if err = s.newAPICall(ctx, account, base, "GET", "/api/token/"+url.PathEscape(key), nil, &token); err != nil {
			return err
		}
		params.Set("token_name", token.Name)
	}
	// Refund events are not gross consumption. Until their linkage is explicit,
	// refuse to publish a misleading net total for a window containing refunds.
	refundParams := url.Values{}
	for k, v := range params {
		refundParams[k] = append([]string{}, v...)
	}
	refundParams.Set("type", "6")
	refundParams.Set("p", "1")
	refundParams.Set("page_size", "1")
	var refunds struct {
		Items []newAPILog `json:"items"`
		Total int         `json:"total"`
	}
	if err = s.newAPICall(ctx, account, base, "GET", "/api/log/self?"+refundParams.Encode(), nil, &refunds); err != nil {
		return err
	}
	if refunds.Total > 0 || len(refunds.Items) > 0 {
		return errors.New("账单包含退款，净消费暂无法确认")
	}
	var list struct {
		Items []newAPILog `json:"items"`
		Total int         `json:"total"`
	}
	if err = s.newAPICall(ctx, account, base, "GET", "/api/log/self?"+params.Encode(), nil, &list); err != nil {
		return err
	}
	if list.Items == nil || list.Total < 0 {
		return errors.New("账单分页信息无效")
	}
	usage := []subUsageLog{}
	spend := []subSpendLog{}
	for _, log := range list.Items {
		if log.Type != 2 || log.Key <= 0 || log.At <= 0 || subSafeRequestID(log.Request, "") == "" || len(log.Request) > 192 {
			return errors.New("账单缺少稳定请求标识")
		}
		receipt, cost, e := newAPIReceipt(log, auth.QuotaPerUnit)
		if e != nil {
			return e
		}
		// Numeric IDs exposed by new-api are page-relative display numbers. Allocate
		// a persistent local ID for the stable request/type/token identity instead.
		var id int64
		err = s.control.db.QueryRowContext(ctx, `INSERT INTO console_site_receipt_ids(account_id,request_id,event_type,key_id) VALUES($1,$2,$3,$4) ON CONFLICT(account_id,request_id,event_type,key_id) DO UPDATE SET request_id=excluded.request_id RETURNING id`, account, log.Request, log.Type, log.Key).Scan(&id)
		if err != nil {
			return err
		}
		receipt.ID = id
		group, e := s.newAPIGroupID(ctx, account, log.Group)
		if e != nil {
			return e
		}
		receipt.GroupID = &group
		usage = append(usage, receipt)
		var meta struct {
			Path string `json:"request_path"`
		}
		_ = json.Unmarshal([]byte(log.Other), &meta)
		spend = append(spend, subSpendLog{ID: id, KeyID: log.Key, GroupID: &group, RequestID: receipt.RequestID, Model: log.Model, Stream: &log.Stream, Endpoint: meta.Path, At: receipt.CreatedAt, Cost: &cost})
	}
	if target, ok := out.(*subSpendPage); ok {
		*target = subSpendPage{Items: spend, Page: page, PageSize: 100, Pages: (list.Total + 99) / 100}
		return nil
	}
	return decodeMap(map[string]any{"items": usage, "page": page, "page_size": 100, "pages": (list.Total + 99) / 100}, out)
}
