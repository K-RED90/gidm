package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	// Blank-imported for its //go:embed directive support.
	_ "embed"

	"github.com/K-RED90/gidm/internal/engine"

	// Registers the pure-Go "sqlite" driver for database/sql.
	_ "modernc.org/sqlite"
)

var _ engine.Store = (*Store)(nil)

//go:embed schema.sql
var schema string

// Applied on every pooled connection: journal_mode is persistent in the file
// header, but foreign_keys and busy_timeout are per-connection and must be set
// here so every connection in the pool enforces cascades and waits on locks.
const pragmas = "_pragma=journal_mode(WAL)" +
	"&_pragma=foreign_keys(1)" +
	"&_pragma=busy_timeout(5000)" +
	"&_pragma=synchronous(NORMAL)"

// Store persists downloads and settings in embedded SQLite. It is safe for
// concurrent use: reads run concurrently under WAL, while mu serializes the
// writers SQLite admits only one of at a time.
type Store struct {
	db *sql.DB
	mu sync.Mutex
}

// New opens (creating if needed) the database at path, applies pragmas, and
// creates the schema idempotently.
func New(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("sqlite: empty database path")
	}

	db, err := sql.Open("sqlite", path+"?"+pragmas)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open %q: %w", path, err)
	}

	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("sqlite: connect: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("sqlite: create schema: %w", err)
	}
	return nil
}

func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("sqlite: close: %w", err)
	}
	return nil
}

// Times are stored as RFC3339 in UTC so they round-trip losslessly to the
// nanosecond and sort lexicographically.
func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(s string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, s)
}
