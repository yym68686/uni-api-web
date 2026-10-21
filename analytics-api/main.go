package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
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
	if len(os.Args) > 1 && os.Args[1] == "migrate-postgresql" {
		if err := migratePostgres(os.Args[2:]); err != nil {
			log.Fatal("PostgreSQL migration failed: ", databaseErrorClass(err))
		}
		return
	}
	cfg := Config{Address: env("LISTEN_ADDR", ":8080"), DataDir: env("DATA_DIR", "./data"), Upstream: strings.TrimRight(env("UNI_API_URL", ""), "/"), SourceID: env("SOURCE_ID", "primary"), Timezone: env("ANALYTICS_TIMEZONE", "Asia/Shanghai")}
	cfg.ControlDatabaseURL, cfg.ControlMasterKey = env("CONTROL_DATABASE_URL", env("DATABASE_URL", "")), env("CONTROL_MASTER_KEY", "")
	cfg.BootstrapSources = env("BOOTSTRAP_SOURCES", "")
	cfg.PublicOrigin = env("PUBLIC_ORIGIN", "")
	cfg.InsecureCookie = env("INSECURE_COOKIE", "false") == "true"
	cfg.AdminUsername, cfg.AdminPassword = env("ADMIN_USERNAME", ""), env("ADMIN_PASSWORD", "")
	if cfg.Upstream == "" && cfg.ControlMasterKey == "" {
		log.Fatal("UNI_API_URL is required")
	}
	if err := os.MkdirAll(cfg.DataDir, 0700); err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	engine, err := OpenEngine("analytics", cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer engine.Close()
	if err = engine.verifyLegacyMigration(ctx); err != nil {
		log.Fatal(err)
	}
	stopDiagnostics := engine.startMemoryDiagnostics(ctx, 5*time.Second)
	defer stopDiagnostics()
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
	backgroundDone := service.startBackground(ctx)
	defer func() { stop(); <-backgroundDone }()
	server := &http.Server{Addr: cfg.Address, Handler: service.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 25 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	if err = service.listenAndServe(ctx, server); err != nil && err != http.ErrServerClosed && ctx.Err() == nil {
		log.Print(err)
		stop()
	}
	// Keep the database open until in-flight HTTP queries have drained.
	<-shutdownDone
}

func (s *Service) listenAndServe(ctx context.Context, server *http.Server) error {
	log.Printf("analytics API listening on %s", server.Addr)
	return server.ListenAndServe()
}
