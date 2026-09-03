package main

import (
	"database/sql"
	"errors"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	migrationsDir := os.Getenv("MIGRATIONS_DIR")
	if migrationsDir == "" {
		migrationsDir = "migrations"
	}
	command := "up"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}

	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return errors.New("initialize migration database connection")
	}
	defer database.Close()
	database.SetMaxOpenConns(1)

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set migration dialect: %w", err)
	}
	switch command {
	case "up":
		if err := goose.Up(database, migrationsDir); err != nil {
			return err
		}
		return grantRuntimePrivileges(database, os.Getenv("APP_DATABASE_ROLE"))
	case "down":
		return goose.Down(database, migrationsDir)
	case "status":
		return goose.Status(database, migrationsDir)
	default:
		return fmt.Errorf("unsupported migration command %q", command)
	}
}

func grantRuntimePrivileges(database *sql.DB, role string) error {
	if role == "" {
		return nil
	}
	quotedRole := pgx.Identifier{role}.Sanitize()
	statements := []string{
		"REVOKE ALL PRIVILEGES ON TABLE links FROM " + quotedRole,
		"GRANT SELECT, INSERT ON TABLE links TO " + quotedRole,
		"REVOKE ALL PRIVILEGES ON FUNCTION public.purge_expired_links(TEXT, TEXT) FROM " + quotedRole,
		"GRANT EXECUTE ON FUNCTION public.purge_expired_links(TEXT, TEXT) TO " + quotedRole,
	}
	for _, statement := range statements {
		if _, err := database.Exec(statement); err != nil {
			return fmt.Errorf("set runtime database privileges: %w", err)
		}
	}
	return nil
}
