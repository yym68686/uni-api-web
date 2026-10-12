package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// The bank and runtime are shared by all concurrent jobs in this backend. Go
// still owns every upstream connection, credential, cancellation and site lease.
type fingerprintProcess struct {
	mu     sync.Mutex
	worker *fingerprintNode
}

type fingerprintNode struct {
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	url, token string
	done       chan struct{}
}

var sharedFingerprintWorker fingerprintProcess
var fingerprintLocalHTTP = &http.Client{
	Transport:     &http.Transport{MaxIdleConns: 50, MaxIdleConnsPerHost: 50, IdleConnTimeout: 30 * time.Second},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func (p *fingerprintProcess) get(ctx context.Context) (*fingerprintNode, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.worker != nil {
		select {
		case <-p.worker.done:
			p.worker = nil
		default:
			return p.worker, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	runner := filepath.Join("fingerprint", "runner.mjs")
	if dir := os.Getenv("MODEL_FINGERPRINT_DIR"); dir != "" {
		runner = filepath.Join(dir, "runner.mjs")
	}
	cmd := exec.Command("node", "--max-old-space-size=192", "--max-semi-space-size=4", runner, "--serve")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "NODE_NO_WARNINGS=1"}
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		return nil, errors.New("指纹检测运行环境不可用")
	}
	w := &fingerprintNode{cmd: cmd, stdin: stdin, done: make(chan struct{})}
	type ready struct {
		Port  int    `json:"port"`
		Token string `json:"token"`
	}
	readiness := make(chan ready, 1)
	go func() {
		var result ready
		line, e := bufio.NewReader(io.LimitReader(stdout, 4096)).ReadBytes('\n')
		if e == nil {
			_ = json.Unmarshal(line, &result)
		}
		readiness <- result
		_ = cmd.Wait()
		stdin.Close()
		close(w.done)
	}()
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	var result ready
	select {
	case result = <-readiness:
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		return nil, ctx.Err()
	case <-timer.C:
		_ = cmd.Process.Kill()
		return nil, errors.New("指纹参考数据校验超时")
	}
	if result.Port < 1 || result.Port > 65535 || len(result.Token) < 16 {
		_ = cmd.Process.Kill()
		return nil, errors.New("指纹参考数据或运行环境校验失败")
	}
	w.url = "http://127.0.0.1:" + strconv.Itoa(result.Port) + "/detect"
	w.token = result.Token
	p.worker = w
	return w, nil
}

func (p *fingerprintProcess) detect(ctx context.Context, config map[string]any) (json.RawMessage, error) {
	w, err := p.get(ctx)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(config)
	if err != nil {
		return nil, errors.New("指纹检测参数无效")
	}
	req, err := http.NewRequestWithContext(ctx, "POST", w.url, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+w.token)
	req.Header.Set("Content-Type", "application/json")
	response, err := fingerprintLocalHTTP.Do(req)
	if err != nil {
		return nil, errors.New("指纹检测执行中断，未自动重发请求")
	}
	defer response.Body.Close()
	result, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || response.StatusCode != 200 || len(result) > 1<<20 {
		return nil, errors.New("指纹检测执行失败，未自动重发请求")
	}
	return json.RawMessage(result), nil
}

func (p *fingerprintProcess) close() {
	p.mu.Lock()
	w := p.worker
	p.worker = nil
	p.mu.Unlock()
	if w != nil {
		_ = w.stdin.Close()
		select {
		case <-w.done:
		case <-time.After(3 * time.Second):
			_ = w.cmd.Process.Kill()
			<-w.done
		}
	}
}
