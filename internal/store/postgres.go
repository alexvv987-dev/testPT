package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/alexvv987-dev/testPt/internal/shortener"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Postgres struct {
	pool         *pgxpool.Pool
	queryTimeout time.Duration
	linkTTL      time.Duration
	maxLinks     int64
}

const saveAdvisoryLockKey int64 = 0x55524c53415645

func NewPostgres(pool *pgxpool.Pool, queryTimeout, linkTTL time.Duration, maxLinks int64) *Postgres {
	return &Postgres{pool: pool, queryTimeout: queryTimeout, linkTTL: linkTTL, maxLinks: maxLinks}
}

func (p *Postgres) Save(ctx context.Context, code, originalURL string) (shortener.SaveResult, error) {
	ctx, cancel := context.WithTimeout(ctx, p.queryTimeout)
	defer cancel()

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return shortener.SaveResult{}, fmt.Errorf("begin save link transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, saveAdvisoryLockKey); err != nil {
		return shortener.SaveResult{}, fmt.Errorf("lock link capacity: %w", err)
	}
	var removed int64
	if err := tx.QueryRow(ctx, `SELECT public.purge_expired_links($1, $2)`, originalURL, code).Scan(&removed); err != nil {
		return shortener.SaveResult{}, fmt.Errorf("purge expired links: %w", err)
	}

	var storedCode string
	const findByURLQuery = `SELECT code FROM links WHERE original_url = $1 AND expires_at > statement_timestamp()`
	err = tx.QueryRow(ctx, findByURLQuery, originalURL).Scan(&storedCode)
	if err == nil {
		return commitSave(ctx, tx, shortener.SaveResult{Code: storedCode})
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return shortener.SaveResult{}, fmt.Errorf("find existing link: %w", err)
	}

	var activeLinks int64
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM links WHERE expires_at > statement_timestamp()`).Scan(&activeLinks); err != nil {
		return shortener.SaveResult{}, fmt.Errorf("count active links: %w", err)
	}
	if activeLinks >= p.maxLinks {
		return commitSave(ctx, tx, shortener.SaveResult{CapacityReached: true})
	}

	const insertQuery = `
		INSERT INTO links (code, original_url, expires_at)
		VALUES ($1, $2, statement_timestamp() + $3 * INTERVAL '1 microsecond')
		ON CONFLICT DO NOTHING
		RETURNING code`

	err = tx.QueryRow(ctx, insertQuery, code, originalURL, p.linkTTL.Microseconds()).Scan(&storedCode)
	if err == nil {
		return commitSave(ctx, tx, shortener.SaveResult{Code: storedCode, Created: true})
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return shortener.SaveResult{}, fmt.Errorf("insert link: %w", err)
	}

	err = tx.QueryRow(ctx, findByURLQuery, originalURL).Scan(&storedCode)
	if err == nil {
		return commitSave(ctx, tx, shortener.SaveResult{Code: storedCode})
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return commitSave(ctx, tx, shortener.SaveResult{CodeCollision: true})
	}
	return shortener.SaveResult{}, fmt.Errorf("find link after conflict: %w", err)
}

func commitSave(ctx context.Context, tx pgx.Tx, result shortener.SaveResult) (shortener.SaveResult, error) {
	if err := tx.Commit(ctx); err != nil {
		return shortener.SaveResult{}, fmt.Errorf("commit save link transaction: %w", err)
	}
	return result, nil
}

func (p *Postgres) FindURL(ctx context.Context, code string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, p.queryTimeout)
	defer cancel()

	const query = `SELECT original_url FROM links WHERE code = $1 AND expires_at > statement_timestamp()`
	var originalURL string
	if err := p.pool.QueryRow(ctx, query, code).Scan(&originalURL); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", shortener.ErrNotFound
		}
		return "", fmt.Errorf("find link by code: %w", err)
	}
	return originalURL, nil
}

func (p *Postgres) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, p.queryTimeout)
	defer cancel()
	return p.pool.Ping(ctx)
}
