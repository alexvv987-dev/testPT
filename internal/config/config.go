package config

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultHTTPAddr       = ":8080"
	defaultPublicBaseURL  = "http://localhost:8080"
	defaultDatabaseURL    = "postgres://shortener:shortener@localhost:5432/shortener?sslmode=disable"
	defaultDBMaxConns     = 10
	defaultDBMinConns     = 1
	defaultDBQueryTimeout = 3 * time.Second
	defaultShutdown       = 10 * time.Second
	defaultRateRPS        = 5.0
	defaultRateBurst      = 10
	minRateRPS            = 0.001
	maxRateRPS            = 10_000.0
)

type Config struct {
	HTTPAddr       string
	PublicBaseURL  string
	DatabaseURL    string
	DBMaxConns     int32
	DBMinConns     int32
	DBQueryTimeout time.Duration
	Shutdown       time.Duration
	RateLimitRPS   float64
	RateLimitBurst int
	LogLevel       string
}

func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:       envOrDefault("HTTP_ADDR", defaultHTTPAddr),
		PublicBaseURL:  envOrDefault("PUBLIC_BASE_URL", defaultPublicBaseURL),
		DatabaseURL:    envOrDefault("DATABASE_URL", defaultDatabaseURL),
		DBMaxConns:     defaultDBMaxConns,
		DBMinConns:     defaultDBMinConns,
		DBQueryTimeout: defaultDBQueryTimeout,
		Shutdown:       defaultShutdown,
		RateLimitRPS:   defaultRateRPS,
		RateLimitBurst: defaultRateBurst,
		LogLevel:       strings.ToLower(envOrDefault("LOG_LEVEL", "info")),
	}

	var err error
	if cfg.DBMaxConns, err = envInt32("DB_MAX_CONNS", cfg.DBMaxConns); err != nil {
		return Config{}, err
	}
	if cfg.DBMinConns, err = envInt32("DB_MIN_CONNS", cfg.DBMinConns); err != nil {
		return Config{}, err
	}
	if cfg.DBQueryTimeout, err = envDuration("DB_QUERY_TIMEOUT", cfg.DBQueryTimeout); err != nil {
		return Config{}, err
	}
	if cfg.Shutdown, err = envDuration("SHUTDOWN_TIMEOUT", cfg.Shutdown); err != nil {
		return Config{}, err
	}
	if cfg.RateLimitRPS, err = envFloat("RATE_LIMIT_RPS", cfg.RateLimitRPS); err != nil {
		return Config{}, err
	}
	if cfg.RateLimitBurst, err = envInt("RATE_LIMIT_BURST", cfg.RateLimitBurst); err != nil {
		return Config{}, err
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	if strings.TrimSpace(c.HTTPAddr) == "" {
		return errors.New("HTTP_ADDR must not be empty")
	}
	if strings.TrimSpace(c.DatabaseURL) == "" {
		return errors.New("DATABASE_URL must not be empty")
	}

	base, err := url.Parse(c.PublicBaseURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return errors.New("PUBLIC_BASE_URL must be an absolute http(s) URL without credentials, query, or fragment")
	}
	if base.Path != "" && base.Path != "/" {
		return errors.New("PUBLIC_BASE_URL must not contain a path")
	}
	c.PublicBaseURL = strings.TrimSuffix(c.PublicBaseURL, "/")

	if c.DBMinConns < 0 || c.DBMaxConns < 1 || c.DBMinConns > c.DBMaxConns {
		return errors.New("database pool limits are invalid")
	}
	if c.DBQueryTimeout <= 0 || c.Shutdown <= 0 {
		return errors.New("timeouts must be positive")
	}
	if math.IsNaN(c.RateLimitRPS) || math.IsInf(c.RateLimitRPS, 0) ||
		c.RateLimitRPS < minRateRPS || c.RateLimitRPS > maxRateRPS || c.RateLimitBurst < 1 {
		return errors.New("rate limit settings are outside the supported range")
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return errors.New("LOG_LEVEL must be debug, info, warn, or error")
	}
	return nil
}

func envOrDefault(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) (int, error) {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return parsed, nil
}

func envInt32(key string, fallback int32) (int32, error) {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%s must be a 32-bit integer: %w", key, err)
	}
	return int32(parsed), nil
}

func envFloat(key string, fallback float64) (float64, error) {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a number: %w", key, err)
	}
	return parsed, nil
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration: %w", key, err)
	}
	return parsed, nil
}
