package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/alexvv987-dev/testPt/internal/shortener"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to PostgreSQL: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping PostgreSQL: %v", err)
	}

	var tableName string
	if err := pool.QueryRow(ctx, `SELECT COALESCE(to_regclass('public.links')::text, '')`).Scan(&tableName); err != nil || tableName != "links" {
		t.Fatalf("links migration is not applied: table=%q error=%v", tableName, err)
	}

	repository := NewPostgres(pool, 3*time.Second)
	suffix := time.Now().UTC().UnixNano()
	firstURL := fmt.Sprintf("https://example.com/integration/%d", suffix)
	secondURL := fmt.Sprintf("https://example.org/integration/%d", suffix)
	cleanup := func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM links WHERE code IN ('int001', 'int002') OR original_url IN ($1, $2)`, firstURL, secondURL)
	}
	cleanup()
	t.Cleanup(cleanup)

	created, err := repository.Save(ctx, "int001", firstURL)
	if err != nil || !created.Created || created.Code != "int001" {
		t.Fatalf("Save(created) = %+v, %v", created, err)
	}
	duplicate, err := repository.Save(ctx, "int002", firstURL)
	if err != nil || duplicate.Created || duplicate.Code != "int001" {
		t.Fatalf("Save(duplicate) = %+v, %v", duplicate, err)
	}
	collision, err := repository.Save(ctx, "int001", secondURL)
	if err != nil || !collision.CodeCollision {
		t.Fatalf("Save(collision) = %+v, %v", collision, err)
	}
	resolved, err := repository.FindURL(ctx, "int001")
	if err != nil || resolved != firstURL {
		t.Fatalf("FindURL() = %q, %v", resolved, err)
	}
	if _, err := repository.FindURL(ctx, "none00"); !errors.Is(err, shortener.ErrNotFound) {
		t.Fatalf("FindURL(missing) error = %v", err)
	}
}

func TestPostgresConcurrentDuplicate(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("connect to PostgreSQL: %v", err)
	}
	t.Cleanup(pool.Close)
	repository := NewPostgres(pool, 5*time.Second)
	originalURL := fmt.Sprintf("https://example.com/concurrent/%d", time.Now().UTC().UnixNano())
	codes := []string{"race01", "race02", "race03", "race04", "race05", "race06", "race07", "race08"}
	cleanup := func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM links WHERE original_url = $1 OR code = ANY($2)`, originalURL, codes)
	}
	cleanup()
	t.Cleanup(cleanup)

	results := make(chan shortener.SaveResult, len(codes))
	errorsChannel := make(chan error, len(codes))
	var waitGroup sync.WaitGroup
	for _, code := range codes {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			result, saveErr := repository.Save(context.Background(), code, originalURL)
			results <- result
			errorsChannel <- saveErr
		}()
	}
	waitGroup.Wait()
	close(results)
	close(errorsChannel)

	for err := range errorsChannel {
		if err != nil {
			t.Fatalf("concurrent Save() error = %v", err)
		}
	}
	createdCount := 0
	storedCode := ""
	for result := range results {
		if result.Created {
			createdCount++
		}
		if storedCode == "" {
			storedCode = result.Code
		}
		if result.Code != storedCode {
			t.Errorf("concurrent code = %q, want %q", result.Code, storedCode)
		}
	}
	if createdCount != 1 {
		t.Fatalf("created count = %d, want 1", createdCount)
	}
}
