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

	repository := NewPostgres(pool, 3*time.Second, 30*24*time.Hour, 1_000_000)
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
	repository := NewPostgres(pool, 5*time.Second, 30*24*time.Hour, 1_000_000)
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

func TestPostgresTTLAndCapacity(t *testing.T) {
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

	firstURL := fmt.Sprintf("https://example.com/ttl/%d", time.Now().UTC().UnixNano())
	secondURL := firstURL + "/capacity"
	codes := []string{"ttl001", "ttl002", "ttl003"}
	cleanup := func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM links WHERE original_url IN ($1, $2) OR code = ANY($3)`, firstURL, secondURL, codes)
	}
	cleanup()
	t.Cleanup(cleanup)

	var activeLinks int64
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM links WHERE expires_at > NOW()`).Scan(&activeLinks); err != nil {
		t.Fatalf("count active links: %v", err)
	}
	repository := NewPostgres(pool, 3*time.Second, 200*time.Millisecond, activeLinks+1)

	created, err := repository.Save(ctx, codes[0], firstURL)
	if err != nil || !created.Created {
		t.Fatalf("Save(first) = %+v, %v", created, err)
	}
	capacity, err := repository.Save(ctx, codes[1], secondURL)
	if err != nil || !capacity.CapacityReached {
		t.Fatalf("Save(capacity) = %+v, %v", capacity, err)
	}

	time.Sleep(400 * time.Millisecond)
	if _, err := repository.FindURL(ctx, codes[0]); !errors.Is(err, shortener.ErrNotFound) {
		t.Fatalf("FindURL(expired) error = %v, want ErrNotFound", err)
	}
	replacement, err := repository.Save(ctx, codes[2], firstURL)
	if err != nil || !replacement.Created || replacement.Code != codes[2] {
		t.Fatalf("Save(replacement) = %+v, %v", replacement, err)
	}
}

func TestPostgresConcurrentCapacity(t *testing.T) {
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

	suffix := time.Now().UTC().UnixNano()
	urls := []string{
		fmt.Sprintf("https://example.com/capacity/a/%d", suffix),
		fmt.Sprintf("https://example.com/capacity/b/%d", suffix),
	}
	codes := []string{"cap001", "cap002"}
	cleanup := func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM links WHERE original_url = ANY($1) OR code = ANY($2)`, urls, codes)
	}
	cleanup()
	t.Cleanup(cleanup)

	var activeLinks int64
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM links WHERE expires_at > NOW()`).Scan(&activeLinks); err != nil {
		t.Fatalf("count active links: %v", err)
	}
	repository := NewPostgres(pool, 5*time.Second, 30*24*time.Hour, activeLinks+1)

	results := make(chan shortener.SaveResult, len(codes))
	errorsChannel := make(chan error, len(codes))
	var waitGroup sync.WaitGroup
	for index := range codes {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			result, saveErr := repository.Save(context.Background(), codes[index], urls[index])
			results <- result
			errorsChannel <- saveErr
		}()
	}
	waitGroup.Wait()
	close(results)
	close(errorsChannel)

	for saveErr := range errorsChannel {
		if saveErr != nil {
			t.Fatalf("concurrent Save() error = %v", saveErr)
		}
	}
	createdCount := 0
	capacityCount := 0
	for result := range results {
		if result.Created {
			createdCount++
		}
		if result.CapacityReached {
			capacityCount++
		}
	}
	if createdCount != 1 || capacityCount != 1 {
		t.Fatalf("concurrent capacity results: created=%d capacity=%d, want 1 and 1", createdCount, capacityCount)
	}
}

func TestPostgresBoundedCleanupPrioritizesCurrentConflict(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to PostgreSQL: %v", err)
	}
	t.Cleanup(pool.Close)

	suffix := time.Now().UTC().UnixNano()
	urlPrefix := fmt.Sprintf("https://example.com/cleanup/%d/", suffix)
	targetURL := urlPrefix + "target"
	cleanup := func() {
		_, _ = pool.Exec(context.Background(), `
			DELETE FROM links
			WHERE original_url LIKE $1
			   OR code BETWEEN 'z00000' AND 'z02499'
			   OR code IN ('targ01', 'fresh1', 'fresh2', 'fresh3')`, urlPrefix+"%")
	}
	cleanup()
	t.Cleanup(cleanup)

	if _, err := pool.Exec(ctx, `
		INSERT INTO links (code, original_url, expires_at)
		SELECT 'z' || lpad(value::text, 5, '0'), $1 || value::text,
		       statement_timestamp() - INTERVAL '100 years'
		FROM generate_series(0, 2499) AS value`, urlPrefix); err != nil {
		t.Fatalf("insert cleanup backlog: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO links (code, original_url, expires_at)
		VALUES ('targ01', $1, statement_timestamp() - INTERVAL '1 second')`, targetURL); err != nil {
		t.Fatalf("insert target conflict: %v", err)
	}

	var activeLinks int64
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM links WHERE expires_at > statement_timestamp()`).Scan(&activeLinks); err != nil {
		t.Fatalf("count active links: %v", err)
	}
	repository := NewPostgres(pool, 3*time.Second, 30*24*time.Hour, activeLinks+10)

	result, err := repository.Save(ctx, "fresh1", targetURL)
	if err != nil || !result.Created || result.Code != "fresh1" {
		t.Fatalf("Save(target conflict) = %+v, %v", result, err)
	}

	var expiredBacklog int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM links
		WHERE original_url LIKE $1 AND expires_at <= statement_timestamp()`, urlPrefix+"%").Scan(&expiredBacklog); err != nil {
		t.Fatalf("count expired backlog: %v", err)
	}
	if expiredBacklog != 1501 {
		t.Fatalf("expired backlog after first batch = %d, want 1501", expiredBacklog)
	}

	for index, code := range []string{"fresh2", "fresh3"} {
		url := fmt.Sprintf("https://example.org/cleanup/%d/%d", suffix, index)
		result, err := repository.Save(ctx, code, url)
		if err != nil || !result.Created {
			t.Fatalf("Save(cleanup batch %d) = %+v, %v", index, result, err)
		}
	}
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM links
		WHERE original_url LIKE $1 AND expires_at <= statement_timestamp()`, urlPrefix+"%").Scan(&expiredBacklog); err != nil {
		t.Fatalf("count final expired backlog: %v", err)
	}
	if expiredBacklog != 0 {
		t.Fatalf("final expired backlog = %d, want 0", expiredBacklog)
	}
}
