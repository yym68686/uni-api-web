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
	cfg.StateBucket, cfg.StateEndpoint, cfg.StatePrefix = env("STATE_S3_BUCKET", ""), env("STATE_S3_ENDPOINT", cfg.S3Endpoint), env("STATE_S3_PREFIX", "analytics/v1")
	cfg.StateAccessKey, cfg.StateSecretKey = env("STATE_AWS_ACCESS_KEY_ID", ""), env("STATE_AWS_SECRET_ACCESS_KEY", "")
	cfg.ControlDatabaseURL, cfg.ControlMasterKey = env("CONTROL_DATABASE_URL", env("DATABASE_URL", "")), env("CONTROL_MASTER_KEY", "")
	cfg.BootstrapSources = env("BOOTSTRAP_SOURCES", "")
	cfg.PublicOrigin = env("PUBLIC_ORIGIN", "")
	cfg.InsecureCookie = env("INSECURE_COOKIE", "false") == "true"
	cfg.AdminUsername, cfg.AdminPassword = env("ADMIN_USERNAME", ""), env("ADMIN_PASSWORD", "")
	cfg.RequireInitialImport = env("REQUIRE_INITIAL_IMPORT", "false") == "true"
	if cfg.RequireInitialImport && (cfg.StateBucket == "" || cfg.StateEndpoint == "" || cfg.S3Bucket == "" || cfg.S3Endpoint == "") {
		log.Fatal("rebuildable analytics requires configured fact and state storage")
	}
	if cfg.Upstream == "" && cfg.ControlMasterKey == "" {
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
	if cfg.ControlMasterKey != "" {
		if cfg.ControlDatabaseURL == "" {
			log.Fatal("CONTROL_DATABASE_URL is required when CONTROL_MASTER_KEY is set")
		}
		service.control, err = newControlStore(cfg.ControlDatabaseURL, cfg.ControlMasterKey)
		if err != nil {
			log.Fatal("initialize control store")
		}
		if err = service.control.ensureBootstrap(cfg.AdminUsername, cfg.AdminPassword); err != nil {
			log.Fatal("initialize administrator")
		}
		defer service.control.Close()
		if err = service.bootstrapSources(ctx); err != nil {
			log.Fatal("initialize source configuration: ", err)
		}
	}
	if cfg.S3Endpoint != "" && cfg.S3Bucket != "" {
		service.factClient, err = newS3Client(ctx, cfg)
		if err != nil {
			log.Fatal("initialize analytics fact client")
		}
	}
	if cfg.StateBucket != "" {
		client, err := newObjectClient(ctx, cfg.StateEndpoint, cfg.StateAccessKey, cfg.StateSecretKey, 2*time.Minute)
		if err != nil {
			log.Fatal("initialize analytics state client")
		}
		service.state = newStateStore(client, cfg)
		service.checkpoints = newCheckpointStore(client, cfg)
		restoreCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		restored, restoreErr := service.checkpoints.restore(restoreCtx, engine)
		cancel()
		if restoreErr != nil {
			log.Print("analytics checkpoint unavailable; rebuilding from raw facts")
		} else if restored {
			log.Print("analytics query cache restored from S3 checkpoint")
		}
		go service.syncStateLoop(ctx)
	}
	// The platform may use a TCP readiness probe. Do not expose a listening
	// socket until the replacement replica can answer from a complete cache.
	if cfg.RequireInitialImport {
		log.Print("analytics warming query cache before accepting traffic")
		for ctx.Err() == nil {
			service.maybeImport(ctx)
			if service.lastCollect.Load() > 0 && (service.state == nil || service.stateReady.Load()) {
				break
			}
			select {
			case <-ctx.Done():
			case <-time.After(5 * time.Second):
			}
		}
		if ctx.Err() != nil {
			return
		}
		log.Print("analytics warming range query caches")
		service.warmAnalytics(ctx)
		if ctx.Err() != nil {
			return
		}
	}
	go service.startImportLoop(ctx)
	if service.control != nil {
		subDone := make(chan struct{})
		go func() { defer close(subDone); service.subWorkerLoop(ctx) }()
		defer func() { stop(); <-subDone }()
	}
	server := &http.Server{Addr: cfg.Address, Handler: service.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 25 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("analytics API listening on %s", cfg.Address)
	if err = server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
	// Keep the database open until in-flight HTTP queries have drained.
	<-shutdownDone
}
