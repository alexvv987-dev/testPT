-- +goose Up
CREATE TABLE links (
    code VARCHAR(6) PRIMARY KEY,
    original_url VARCHAR(2048) NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT links_code_format CHECK (code ~ '^[0-9A-Za-z]{6}$')
);

-- +goose Down
DROP TABLE links;
