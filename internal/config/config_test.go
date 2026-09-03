package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	for _, key := range []string{
		"HTTP_ADDR", "PUBLIC_BASE_URL", "DATABASE_URL", "DB_MAX_CONNS", "DB_MIN_CONNS",
		"DB_QUERY_TIMEOUT", "SHUTDOWN_TIMEOUT", "RATE_LIMIT_RPS", "RATE_LIMIT_BURST", "LOG_LEVEL",
		"READ_RATE_LIMIT_RPS", "READ_RATE_LIMIT_BURST", "GLOBAL_RATE_LIMIT_RPS", "GLOBAL_RATE_LIMIT_BURST",
		"MAX_CONCURRENT_REQUESTS", "MAX_CONNECTIONS", "LINK_TTL", "MAX_LINKS",
	} {
		t.Setenv(key, "")
		_ = key
	}

	// Explicit values avoid inheriting developer-machine configuration.
	t.Setenv("HTTP_ADDR", ":8081")
	t.Setenv("PUBLIC_BASE_URL", "https://short.example/")
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("DB_MAX_CONNS", "8")
	t.Setenv("DB_MIN_CONNS", "2")
	t.Setenv("DB_QUERY_TIMEOUT", "2s")
	t.Setenv("SHUTDOWN_TIMEOUT", "5s")
	t.Setenv("RATE_LIMIT_RPS", "3")
	t.Setenv("RATE_LIMIT_BURST", "4")
	t.Setenv("READ_RATE_LIMIT_RPS", "30")
	t.Setenv("READ_RATE_LIMIT_BURST", "60")
	t.Setenv("GLOBAL_RATE_LIMIT_RPS", "300")
	t.Setenv("GLOBAL_RATE_LIMIT_BURST", "600")
	t.Setenv("MAX_CONCURRENT_REQUESTS", "50")
	t.Setenv("MAX_CONNECTIONS", "75")
	t.Setenv("LINK_TTL", "24h")
	t.Setenv("MAX_LINKS", "1000")
	t.Setenv("LOG_LEVEL", "debug")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.PublicBaseURL != "https://short.example" || cfg.DBMaxConns != 8 || cfg.DBMinConns != 2 {
		t.Fatalf("Load() = %+v", cfg)
	}
	if cfg.ReadRateRPS != 30 || cfg.ReadRateBurst != 60 || cfg.GlobalRateRPS != 300 || cfg.GlobalBurst != 600 {
		t.Fatalf("Load() rate limits = %+v", cfg)
	}
	if cfg.MaxConcurrent != 50 || cfg.MaxConnections != 75 || cfg.LinkTTL != 24*time.Hour || cfg.MaxLinks != 1000 {
		t.Fatalf("Load() resource limits = %+v", cfg)
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "base url path", key: "PUBLIC_BASE_URL", value: "https://example.com/path"},
		{name: "base url scheme", key: "PUBLIC_BASE_URL", value: "ftp://example.com"},
		{name: "pool limit", key: "DB_MAX_CONNS", value: "0"},
		{name: "duration", key: "SHUTDOWN_TIMEOUT", value: "never"},
		{name: "rate", key: "RATE_LIMIT_RPS", value: "0"},
		{name: "rate NaN", key: "RATE_LIMIT_RPS", value: "NaN"},
		{name: "rate positive infinity", key: "RATE_LIMIT_RPS", value: "+Inf"},
		{name: "rate too small", key: "RATE_LIMIT_RPS", value: "1e-320"},
		{name: "rate too large", key: "RATE_LIMIT_RPS", value: "10001"},
		{name: "read rate", key: "READ_RATE_LIMIT_RPS", value: "0"},
		{name: "global rate", key: "GLOBAL_RATE_LIMIT_RPS", value: "+Inf"},
		{name: "concurrency", key: "MAX_CONCURRENT_REQUESTS", value: "0"},
		{name: "connections", key: "MAX_CONNECTIONS", value: "0"},
		{name: "ttl", key: "LINK_TTL", value: "0s"},
		{name: "ttl too long", key: "LINK_TTL", value: "8761h"},
		{name: "capacity", key: "MAX_LINKS", value: "0"},
		{name: "log level", key: "LOG_LEVEL", value: "verbose"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(test.key, test.value)
			if _, err := Load(); err == nil {
				t.Fatalf("Load() unexpectedly succeeded for %s=%q", test.key, test.value)
			}
		})
	}
}
