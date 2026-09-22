// Command permeable runs the calendar aggregator: it wires together
// config, the SQLite database, the two HTTP listeners (admin UI + public
// feed), and the background scheduler. Kept intentionally thin — see
// internal/ for the actual logic.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"permeable/internal/config"
	"permeable/internal/db"
	"permeable/internal/scheduler"
	"permeable/internal/web"
)

// shutdownTimeout bounds how long a SIGTERM/SIGINT graceful shutdown
// waits for in-flight requests before giving up.
const shutdownTimeout = 10 * time.Second

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("permeable: config: %v", err)
	}

	sqlDB, err := db.Open(cfg.DBPath, cfg.InitialRefreshMinutes)
	if err != nil {
		log.Fatalf("permeable: db: %v", err)
	}
	defer sqlDB.Close()
	log.Printf("permeable: db ready at %s (migrations applied)", cfg.DBPath)

	// Canceled on SIGINT/SIGTERM (Ctrl+C, `docker stop`) — the scheduler
	// and both HTTP servers below shut down cleanly on this signal
	// rather than being killed mid-request/mid-generate.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	sched := scheduler.New(sqlDB)
	go sched.Run(ctx)

	handler, err := web.NewHandler(sqlDB, cfg.FeedPort, sched)
	if err != nil {
		log.Fatalf("permeable: web: %v", err)
	}

	adminServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.AdminPort),
		Handler: handler.AdminRoutes(),
	}
	feedServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.FeedPort),
		Handler: handler.FeedRoutes(),
	}

	// Two independent listeners in one process: admin (LAN-only, never
	// reverse-proxied) and feed (the one port safe to expose publicly).
	// See CLAUDE.md "Endpoints" for the split rationale.
	errCh := make(chan error, 2)
	go func() {
		log.Printf("permeable: admin UI listening on %s (keep LAN-only)", adminServer.Addr)
		if err := adminServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("admin server: %w", err)
		}
	}()
	go func() {
		log.Printf("permeable: feed listening on %s (safe to reverse-proxy)", feedServer.Addr)
		if err := feedServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("feed server: %w", err)
		}
	}()

	select {
	case err := <-errCh:
		log.Fatalf("permeable: %v", err)
	case <-ctx.Done():
		log.Printf("permeable: shutdown signal received, shutting down (up to %s)", shutdownTimeout)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := adminServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("permeable: admin server shutdown: %v", err)
	}
	if err := feedServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("permeable: feed server shutdown: %v", err)
	}
	log.Printf("permeable: shutdown complete")
}
