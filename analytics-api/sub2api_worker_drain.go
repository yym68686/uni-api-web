package main

import (
	"bufio"
	"context"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const subDrainMetricsURL = "http://127.0.0.1:19090/metrics"

// The Fugue preStop hook drains HTTP before delivering SIGTERM. It records
// the start in the local sidecar's counter; never call the preStop endpoint.
// Other runtimes have no sidecar and still recover on SIGTERM or lease expiry.
var subDrainHTTP = &http.Client{
	Transport: &http.Transport{DisableKeepAlives: true},
	Timeout:   250 * time.Millisecond,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

func subDrainRequested(ctx context.Context, endpoint string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false
	}
	res, err := subDrainHTTP.Do(req)
	if err != nil {
		return false
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return false
	}
	scan := bufio.NewScanner(io.LimitReader(res.Body, 64<<10))
	for scan.Scan() {
		fields := strings.Fields(scan.Text())
		if len(fields) == 2 && fields[0] == "fugue_app_drain_prestop_requests_total" {
			n, err := strconv.ParseInt(fields[1], 10, 64)
			return err == nil && n > 0
		}
	}
	return false
}

func subWatchDrain(ctx context.Context, probe func(context.Context) bool, stop context.CancelFunc) {
	for waitStartup(ctx, time.Second) {
		if probe(ctx) {
			log.Print("event=site_detection_worker_draining")
			stop()
			return
		}
	}
}
