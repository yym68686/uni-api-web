package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func env(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}
func main() {
	cfg := Config{Address: env("LISTEN_ADDR", ":8080"), DataDir: env("DATA_DIR", "./data"), Upstream: strings.TrimRight(env("UNI_API_URL", ""), "/"), SourceID: env("SOURCE_ID", "primary"), Timezone: env("ANALYTICS_TIMEZONE", "Asia/Shanghai"), S3Endpoint: env("S3_ENDPOINT", ""), S3Bucket: env("S3_BUCKET", ""), S3Prefix: env("S3_PREFIX", "uni-api-facts/v1/"), Poll: 5 * time.Second}
	if cfg.Upstream == "" {
		log.Fatal("UNI_API_URL is required")
	}
	if err := os.MkdirAll(cfg.DataDir, 0700); err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	engine, err := OpenEngine(filepath.Join(cfg.DataDir, "analytics.duckdb"), cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer engine.Close()
	service, err := NewService(engine, cfg)
	if err != nil {
		log.Fatal(err)
	}
	go service.startImportLoop(ctx)
	server := &http.Server{Addr: cfg.Address, Handler: service.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 25 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("analytics API listening on %s", cfg.Address)
	if err = server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
