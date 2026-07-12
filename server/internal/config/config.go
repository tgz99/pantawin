// Package config loads server configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	HTTPAddr    string // e.g. "127.0.0.1:8080" — bind loopback-only, nginx proxies to it
	DatabaseURL string
	RedisAddr   string
	RedisDB     int
	JWTSecret   string

	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration

	// Bootstrap admin account, created on first boot if no users exist yet.
	AdminEmail    string
	AdminPassword string

	// M0 seed monitor.
	SeedMonitorName string
	SeedMonitorURL  string

	CheckTimeout time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:        getEnv("HTTP_ADDR", "127.0.0.1:8080"),
		DatabaseURL:     os.Getenv("DATABASE_URL"),
		RedisAddr:       getEnv("REDIS_ADDR", "redis:6379"),
		JWTSecret:       os.Getenv("JWT_SECRET"),
		AccessTokenTTL:  15 * time.Minute,
		RefreshTokenTTL: 30 * 24 * time.Hour,
		AdminEmail:      os.Getenv("ADMIN_EMAIL"),
		AdminPassword:   os.Getenv("ADMIN_PASSWORD"),
		SeedMonitorName: getEnv("SEED_MONITOR_NAME", "gratisaja.com"),
		SeedMonitorURL:  getEnv("SEED_MONITOR_URL", "https://gratisaja.com"),
		CheckTimeout:    10 * time.Second,
	}

	if redisDBStr := os.Getenv("REDIS_DB"); redisDBStr != "" {
		n, err := strconv.Atoi(redisDBStr)
		if err != nil {
			return Config{}, fmt.Errorf("invalid REDIS_DB: %w", err)
		}
		cfg.RedisDB = n
	}

	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.JWTSecret == "" {
		return Config{}, fmt.Errorf("JWT_SECRET is required")
	}
	if cfg.AdminEmail == "" || cfg.AdminPassword == "" {
		return Config{}, fmt.Errorf("ADMIN_EMAIL and ADMIN_PASSWORD are required for bootstrap")
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
