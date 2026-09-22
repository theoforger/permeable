// Package config loads process-level settings (listen port, DB path, and
// the initial poll interval used to seed the config table) from CLI flags
// and an optional .env file. Runtime-adjustable settings (poll interval
// after first run, feed TTL ceiling, date window, feed title) live in the
// `config` DB table instead, and are edited via the settings UI.
package config

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

// Config holds process-level settings resolved from flags and environment
// variables (including an optional .env file), in that precedence order.
type Config struct {
	// AdminPort serves the settings UI and all CRUD/stats routes. Keep
	// this LAN-only — never put it behind a public reverse proxy.
	AdminPort int
	// FeedPort serves only GET /feed.ics. This is the one port meant to
	// be reverse-proxied / exposed publicly.
	FeedPort              int
	DBPath                string
	InitialRefreshMinutes int
}

// Load reads an optional .env file into the process environment, then
// parses flags (falling back to environment variables, then hardcoded
// defaults) into a Config.
func Load() (*Config, error) {
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("load .env: %w", err)
	}

	cfg := &Config{}

	fs := flag.NewFlagSet("permeable", flag.ContinueOnError)
	fs.IntVar(&cfg.AdminPort, "admin-port", envInt("PERMEABLE_ADMIN_PORT", 8080),
		"HTTP listen port for the admin UI (settings, CRUD, stats) — keep this LAN-only")
	fs.IntVar(&cfg.FeedPort, "feed-port", envInt("PERMEABLE_FEED_PORT", 8081),
		"HTTP listen port that serves only GET /feed.ics — the one port safe to reverse-proxy publicly")
	fs.StringVar(&cfg.DBPath, "db", envString("PERMEABLE_DB_PATH", "data/permeable.db"),
		"path to the SQLite database file")
	fs.IntVar(&cfg.InitialRefreshMinutes, "refresh-minutes", envInt("PERMEABLE_REFRESH_MINUTES", 60),
		"initial poll interval in minutes, used only to seed the config table on first run")

	if err := fs.Parse(os.Args[1:]); err != nil {
		return nil, fmt.Errorf("parse flags: %w", err)
	}

	if cfg.AdminPort == cfg.FeedPort {
		return nil, fmt.Errorf("admin-port and feed-port must differ (both %d) — "+
			"they back separate listeners so the feed can be exposed without the admin UI", cfg.AdminPort)
	}

	return cfg, nil
}

func envString(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	v, ok := os.LookupEnv(key)
	if !ok {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
