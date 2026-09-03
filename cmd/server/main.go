package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alexvv987-dev/testPt/internal/config"
	"github.com/alexvv987-dev/testPt/internal/httpapi"
	"github.com/alexvv987-dev/testPt/internal/shortener"
	"github.com/alexvv987-dev/testPt/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		if err := checkHealth(); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func checkHealth() error {
	return checkHealthURL("http://127.0.0.1:8080/healthz")
}

func checkHealthURL(target string) error {
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(target)
	if err != nil {
		return errors.New("health endpoint is unavailable")
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("health endpoint returned status %d", response.StatusCode)
	}
	return nil
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := newLogger(cfg.LogLevel)
	slog.SetDefault(logger)

	poolConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return errors.New("invalid database configuration")
	}
	poolConfig.MaxConns = cfg.DBMaxConns
	poolConfig.MinConns = cfg.DBMinConns

	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		return errors.New("initialize database pool")
	}
	defer pool.Close()

	startupCtx, startupCancel := context.WithTimeout(context.Background(), cfg.DBQueryTimeout)
	defer startupCancel()
	if err := pool.Ping(startupCtx); err != nil {
		return errors.New("database is unavailable")
	}

	repository := store.NewPostgres(pool, cfg.DBQueryTimeout, cfg.LinkTTL, cfg.MaxLinks)
	service := shortener.NewService(repository, shortener.URLValidator{}, shortener.NewRandomGenerator())
	postLimiter := httpapi.NewClientLimiter(cfg.RateLimitRPS, cfg.RateLimitBurst)
	defer postLimiter.Close()
	readLimiter := httpapi.NewClientLimiter(cfg.ReadRateRPS, cfg.ReadRateBurst)
	defer readLimiter.Close()
	globalGuard := httpapi.NewGlobalGuard(cfg.GlobalRateRPS, cfg.GlobalBurst, cfg.MaxConcurrent)

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpapi.New(service, repository, cfg.PublicBaseURL, logger, postLimiter, readLimiter, globalGuard),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 * 1024,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
	listener, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listen on configured address: %w", err)
	}
	limitedListener := httpapi.LimitListener(listener, cfg.MaxConnections)
	defer limitedListener.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("server started", "address", cfg.HTTPAddr)
		serverErrors <- server.Serve(limitedListener)
	}()

	select {
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		logger.Info("shutdown requested")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Shutdown)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return err
	}
	logger.Info("server stopped gracefully")
	return nil
}

func newLogger(level string) *slog.Logger {
	var slogLevel slog.Level
	switch level {
	case "debug":
		slogLevel = slog.LevelDebug
	case "warn":
		slogLevel = slog.LevelWarn
	case "error":
		slogLevel = slog.LevelError
	default:
		slogLevel = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slogLevel}))
}
