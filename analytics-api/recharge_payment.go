package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type rechargeWallet struct {
	Status    string `json:"status"`
	Message   string `json:"message"`
	ActionURL string `json:"action_url,omitempty"`
}
type rechargePayment interface {
	Configured(string) bool
	Wallet(context.Context, string, string, string) (rechargeWallet, error)
	Submit(context.Context, string, string, string) (string, error)
}
type alipayRechargePayment struct{ binary, owner, session, home string }

// One OS wallet belongs to exactly one console owner, not to every logged-in
// user. Multi-owner deployments must run separate isolated payment workers.
// There is no runtime install, shell command, password storage or wallet reuse.
func (s *Service) rechargePayments() rechargePayment {
	if s.rechargePayment != nil {
		return s.rechargePayment
	}
	return alipayRechargePayment{os.Getenv("RECHARGE_ALIPAY_BIN"), os.Getenv("RECHARGE_ALIPAY_OWNER"), os.Getenv("RECHARGE_ALIPAY_SESSION_ID"), os.Getenv("RECHARGE_ALIPAY_HOME")}
}
func (p alipayRechargePayment) Configured(owner string) bool {
	if owner == "" || p.owner != owner || !filepath.IsAbs(p.binary) || p.session == "" || !filepath.IsAbs(p.home) {
		return false
	}
	home, err := os.Stat(p.home)
	if err != nil || !home.IsDir() || home.Mode().Perm()&0077 != 0 {
		return false
	}
	info, err := os.Stat(p.binary)
	return err == nil && !info.IsDir() && info.Mode()&0111 != 0
}

type rechargeOutput struct{ bytes.Buffer }

func (b *rechargeOutput) Write(raw []byte) (int, error) {
	if b.Len()+len(raw) > 64<<10 {
		return 0, errors.New("payment output too large")
	}
	return b.Buffer.Write(raw)
}
func (p alipayRechargePayment) run(parent context.Context, owner string, args ...string) ([]byte, error) {
	if !p.Configured(owner) {
		return nil, errors.New("支付宝支付代理未配置；未创建订单或发起付款")
	}
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, p.binary, args...)
	cmd.WaitDelay = 2 * time.Second
	// Never inherit the analytics service's database, platform or site secrets.
	cmd.Env = []string{"HOME=" + p.home, "XDG_CONFIG_HOME=" + filepath.Join(p.home, ".config"), "XDG_DATA_HOME=" + filepath.Join(p.home, ".local", "share"), "PATH=" + os.Getenv("PATH"), "LANG=C.UTF-8"}
	var out rechargeOutput
	cmd.Stdout = &out
	cmd.Stderr = io.Discard // upstream diagnostics can contain payment credentials
	if err := cmd.Run(); err != nil {
		return nil, errors.New("支付宝命令未确认完成，请只核对原订单，勿重复付款")
	}
	return out.Bytes(), nil
}
func rechargeOfficialAction(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.User == nil && u.Port() == "" && u.Fragment == "" && (u.Hostname() == "aipay.alipay.com" || rechargeAlipayURL(raw))
}
func rechargeAction(raw []byte) string {
	links := regexp.MustCompile(`https://[^\s<>"'\x60]+`).FindAllString(string(raw), -1)
	for _, link := range links {
		link = strings.TrimRight(link, ").，。")
		if rechargeOfficialAction(link) {
			return link
		}
	}
	return ""
}

// Official check-wallet documents the last result JSON. Do not infer a bound
// wallet from a success-looking line, an exit code, or an authorization link.
func rechargeWalletJSON(raw []byte) rechargeWallet {
	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	}
	for i := len(raw) - 1; i >= 0; i-- {
		if raw[i] != '{' {
			continue
		}
		if json.Unmarshal(bytes.TrimSpace(raw[i:]), &result) == nil {
			if result.Code == 200 && result.Message == "已开启支付宝支付功能" {
				return rechargeWallet{Status: "bound", Message: "支付宝支付功能已开启"}
			}
			if result.Code == 200 && result.Status == "applied_unbound" {
				return rechargeWallet{Status: "pending", Message: "等待支付宝官方授权"}
			}
			if result.Code == 500 && result.Message == "未开通" {
				return rechargeWallet{Status: "unbound", Message: "尚未开通支付宝支付功能"}
			}
			break
		}
	}
	return rechargeWallet{Status: "unknown", Message: "钱包状态尚未确认，请核对官方授权结果"}
}
func (p alipayRechargePayment) Wallet(ctx context.Context, owner, action, code string) (rechargeWallet, error) {
	args := []string{"check-wallet"}
	switch action {
	case "apply":
		args = []string{"apply-wallet", "--agent-name", "uni-api-web"}
	case "bind":
		if strings.TrimSpace(code) == "" || len(code) > 256 || strings.ContainsAny(code, "\r\n\x00") {
			return rechargeWallet{}, errors.New("请输入本次开通过程的授权码")
		}
		args = []string{"bind-wallet", "-c", code}
	case "status":
	default:
		return rechargeWallet{}, errors.New("不支持的钱包操作")
	}
	out, err := p.run(ctx, owner, args...)
	if err != nil {
		return rechargeWallet{}, err
	}
	result := rechargeWalletJSON(out)
	if action == "apply" && result.Status != "bound" {
		if link := rechargeAction(out); link != "" {
			result = rechargeWallet{Status: "pending", Message: "请完成支付宝官方授权，再提交本次授权码或查询状态", ActionURL: link}
		}
	}
	return result, nil
}
func (p alipayRechargePayment) Submit(ctx context.Context, owner, link, intent string) (string, error) {
	if !rechargeAlipayURL(link) {
		return "", errors.New("不是已适配的支付宝官方收银台")
	}
	out, err := p.run(ctx, owner, "submit-payment", "--session-id", p.session, "--payment-link", link, "--intent-summary", intent)
	if err != nil {
		return "", err
	}
	// Do not parse CLI text as proof of payment. The merchant's exact paid order
	// is the independent authority for delivery, including after CLI timeouts.
	return rechargeAction(out), nil
}
