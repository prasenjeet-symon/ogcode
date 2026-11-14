package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"log/slog"

	_ "modernc.org/sqlite"

	"github.com/pressly/goose/v3"
)

//go:embed *.sql
var embedMigrations embed.FS

type DB struct {
	*sql.DB
}

// gooseLog adapts goose's Logger interface to slog, so migration output lands
// in the same log file everything else uses instead of being discarded. The
// interface requires both Printf and Fatalf; Fatalf is written at Error level
// and does NOT exit — a failed migration already returns an error from
// Provider.Up, which Open reports, and the server must not die inside Open.
type gooseLog struct{}

func (gooseLog) Printf(format string, v ...any) {
	slog.Info(fmt.Sprintf(format, v...))
}

func (gooseLog) Fatalf(format string, v ...any) {
	slog.Error(fmt.Sprintf(format, v...))
}

func Open(path string) (*DB, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	goose.SetDialect("sqlite3")

	// WithVerbose(true) matters: the provider gates every log call behind
	// verbose even when a custom logger is attached, so without it no
	// migration output ever reaches the adapter.
	gooseProvider, err := goose.NewProvider(goose.DialectSQLite3, db, embedMigrations,
		goose.WithLogger(gooseLog{}), goose.WithVerbose(true))
	if err != nil {
		return nil, fmt.Errorf("create goose provider: %w", err)
	}

	results, err := gooseProvider.Up(context.Background())
	if err != nil {
		return nil, fmt.Errorf("run migrations: %w", err)
	}
	slog.Info("migrations applied", "count", len(results))

	slog.Info("database opened", "path", path)
	return &DB{DB: db}, nil
}
