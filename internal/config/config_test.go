package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	for _, key := range []string{
		"HTTP_ADDR", "PUBLIC_BASE_URL", "DATABASE_URL", "DB_MAX_CONNS", "DB_MIN_CONNS",
		"DB_QUERY_TIMEOUT", "SHUTDOWN_TIMEOUT", "RATE_LIMIT_RPS", "RATE_LIMIT_BURST", "LOG_LEVEL",
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
	t.Setenv("LOG_LEVEL", "debug")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.PublicBaseURL != "https://short.example" || cfg.DBMaxConns != 8 || cfg.DBMinConns != 2 {
		t.Fatalf("Load() = %+v", cfg)
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
