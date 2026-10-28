package store

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"hearth/internal/token"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

type Store struct {
	db *sql.DB
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	var fk int
	if err := db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil {
		_ = db.Close()
		return nil, err
	}
	if fk != 1 {
		_ = db.Close()
		return nil, errors.New("sqlite foreign keys are disabled")
	}
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return err
	}
	var v1, v2, v3 int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = 1`).Scan(&v1); err != nil {
		return err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = 2`).Scan(&v2); err != nil {
		return err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = 3`).Scan(&v3); err != nil {
		return err
	}
	if v3 > 0 {
		return nil
	}
	if v1 == 0 && v2 == 0 {
		if err := execScript(ctx, s.db, schemaSQL); err != nil {
			return err
		}
	} else {
		if v2 == 0 {
			if err := s.migrateV2(ctx); err != nil {
				return err
			}
		}
		if err := s.migrateV3(ctx); err != nil {
			return err
		}
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (3, ?)`, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) migrateV3(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE bill_shares ADD COLUMN paid INTEGER NOT NULL DEFAULT 0 CHECK (paid IN (0, 1))`); err != nil {
		return fmt.Errorf("migrate v3: %w", err)
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE bill_shares SET paid = 1
		WHERE amount_cents <= (
			SELECT COALESCE(SUM(amount_cents), 0)
			FROM share_payments sp
			WHERE sp.bill_id = bill_shares.bill_id AND sp.participant_id = bill_shares.participant_id
		) AND amount_cents > 0 AND EXISTS (
			SELECT 1 FROM share_payments sp
			WHERE sp.bill_id = bill_shares.bill_id AND sp.participant_id = bill_shares.participant_id
		)`)
	if err != nil {
		return fmt.Errorf("migrate v3: %w", err)
	}
	return nil
}

func (s *Store) migrateV2(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		return err
	}
	defer func() { _, _ = s.db.ExecContext(ctx, `PRAGMA foreign_keys=ON`) }()
	for _, stmt := range migrateV2SQL {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			preview := stmt
			if len(preview) > 80 {
				preview = preview[:80]
			}
			return fmt.Errorf("migrate v2: %s: %w", preview, err)
		}
	}
	rows, err := s.db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return errors.New("migrate v2: foreign key check failed")
	}
	return rows.Err()
}

func (s *Store) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func dsn(path string) string {
	p := filepath.ToSlash(path)
	p = strings.ReplaceAll(p, "?", "%3F")
	p = strings.ReplaceAll(p, "#", "%23")
	return "file:" + p + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
}

func execScript(ctx context.Context, db *sql.DB, script string) error {
	for _, stmt := range strings.Split(script, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			preview := stmt
			if len(preview) > 80 {
				preview = preview[:80]
			}
			return fmt.Errorf("migrate: %s: %w", preview, err)
		}
	}
	return nil
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique")
}

func newID() (string, error) {
	return token.NewID()
}

func hashToken(raw string) string {
	return token.Hash(raw)
}

func parseTS(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, s)
}

func parseDate(s string) (time.Time, error) {
	return time.ParseInLocation("2006-01-02", s, time.Local)
}

func fmtTS(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func fmtDate(t time.Time) string {
	return t.Format("2006-01-02")
}

var migrateV2SQL = []string{
	`CREATE TABLE IF NOT EXISTS participants (
		id TEXT PRIMARY KEY,
		household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
		name TEXT NOT NULL,
		email TEXT,
		user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
		active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0, 1)),
		created_at TEXT NOT NULL
	)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_participants_user ON participants(household_id, user_id) WHERE user_id IS NOT NULL`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_participants_email ON participants(household_id, email) WHERE email IS NOT NULL AND email != ''`,
	`INSERT INTO participants (id, household_id, name, email, user_id, active, created_at)
		SELECT lower(hex(randomblob(16))), m.household_id, u.name, u.email, u.id, 1, m.created_at
		FROM memberships m
		JOIN users u ON u.id = m.user_id`,
	`CREATE TABLE bill_shares_v2 (
		bill_id TEXT NOT NULL REFERENCES bills(id) ON DELETE CASCADE,
		participant_id TEXT NOT NULL REFERENCES participants(id),
		amount_cents INTEGER NOT NULL CHECK (amount_cents >= 0),
		weight INTEGER NOT NULL DEFAULT 0 CHECK (weight >= 0),
		PRIMARY KEY (bill_id, participant_id)
	)`,
	`INSERT INTO bill_shares_v2 (bill_id, participant_id, amount_cents, weight)
		SELECT s.bill_id, p.id, s.amount_cents, s.weight
		FROM bill_shares s
		JOIN bills b ON b.id = s.bill_id
		JOIN participants p ON p.user_id = s.user_id AND p.household_id = b.household_id`,
	`CREATE TABLE share_payments_v2 (
		id TEXT PRIMARY KEY,
		bill_id TEXT NOT NULL,
		participant_id TEXT NOT NULL,
		amount_cents INTEGER NOT NULL CHECK (amount_cents > 0),
		note TEXT NOT NULL DEFAULT '',
		recorded_by TEXT NOT NULL REFERENCES users(id),
		created_at TEXT NOT NULL,
		FOREIGN KEY (bill_id, participant_id) REFERENCES bill_shares_v2(bill_id, participant_id)
	)`,
	`INSERT INTO share_payments_v2 (id, bill_id, participant_id, amount_cents, note, recorded_by, created_at)
		SELECT sp.id, sp.bill_id, p.id, sp.amount_cents, sp.note, sp.recorded_by, sp.created_at
		FROM share_payments sp
		JOIN bills b ON b.id = sp.bill_id
		JOIN participants p ON p.user_id = sp.user_id AND p.household_id = b.household_id`,
	`DROP TABLE share_payments`,
	`DROP TABLE bill_shares`,
	`ALTER TABLE bill_shares_v2 RENAME TO bill_shares`,
	`ALTER TABLE share_payments_v2 RENAME TO share_payments`,
	`ALTER TABLE bills ADD COLUMN paid_by_participant_id TEXT REFERENCES participants(id)`,
	`UPDATE bills SET paid_by_participant_id = (
		SELECT p.id FROM participants p
		WHERE p.user_id = bills.paid_by_user_id AND p.household_id = bills.household_id
	) WHERE paid_by_user_id IS NOT NULL`,
	`ALTER TABLE bills DROP COLUMN paid_by_user_id`,
	`CREATE TABLE IF NOT EXISTS utility_exemptions (
		utility_id TEXT NOT NULL REFERENCES utilities(id) ON DELETE CASCADE,
		participant_id TEXT NOT NULL REFERENCES participants(id) ON DELETE CASCADE,
		PRIMARY KEY (utility_id, participant_id)
	)`,
	`CREATE TABLE IF NOT EXISTS oauth_codes (
		code_hash TEXT PRIMARY KEY,
		client_id TEXT NOT NULL,
		user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		redirect_uri TEXT NOT NULL,
		invite_token TEXT NOT NULL DEFAULT '',
		expires_at TEXT NOT NULL,
		created_at TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS oauth_refresh_tokens (
		token_hash TEXT PRIMARY KEY,
		family_id TEXT NOT NULL,
		client_id TEXT NOT NULL,
		user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		household_id TEXT,
		expires_at TEXT NOT NULL,
		revoked_at TEXT,
		created_at TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_shares_participant ON bill_shares(participant_id)`,
	`CREATE INDEX IF NOT EXISTS idx_participants_household ON participants(household_id)`,
	`CREATE INDEX IF NOT EXISTS idx_refresh_family ON oauth_refresh_tokens(family_id)`,
	`CREATE INDEX IF NOT EXISTS idx_payments_bill ON share_payments(bill_id, participant_id)`,
	`DROP TABLE IF EXISTS sessions`,
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	b := strings.Repeat("?,", n)
	return b[:len(b)-1]
}
