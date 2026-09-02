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
}

func NewPostgres(pool *pgxpool.Pool, queryTimeout time.Duration) *Postgres {
	return &Postgres{pool: pool, queryTimeout: queryTimeout}
}

func (p *Postgres) Save(ctx context.Context, code, originalURL string) (shortener.SaveResult, error) {
	ctx, cancel := context.WithTimeout(ctx, p.queryTimeout)
	defer cancel()

	const insertQuery = `
		INSERT INTO links (code, original_url)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING
		RETURNING code`

	var storedCode string
	err := p.pool.QueryRow(ctx, insertQuery, code, originalURL).Scan(&storedCode)
	if err == nil {
		return shortener.SaveResult{Code: storedCode, Created: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return shortener.SaveResult{}, fmt.Errorf("insert link: %w", err)
	}

	const findByURLQuery = `SELECT code FROM links WHERE original_url = $1`
	err = p.pool.QueryRow(ctx, findByURLQuery, originalURL).Scan(&storedCode)
	if err == nil {
		return shortener.SaveResult{Code: storedCode}, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return shortener.SaveResult{CodeCollision: true}, nil
	}
	return shortener.SaveResult{}, fmt.Errorf("find link after conflict: %w", err)
}

func (p *Postgres) FindURL(ctx context.Context, code string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, p.queryTimeout)
	defer cancel()

	const query = `SELECT original_url FROM links WHERE code = $1`
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
