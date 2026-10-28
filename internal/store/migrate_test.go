package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestMigrateV1KeepsBills(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hearth.db")
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, stmt := range []string{
		`CREATE TABLE users (
			id TEXT PRIMARY KEY, email TEXT NOT NULL, name TEXT NOT NULL, password_hash TEXT NOT NULL, created_at TEXT NOT NULL)`,
		`CREATE TABLE households (
			id TEXT PRIMARY KEY, name TEXT NOT NULL, currency TEXT NOT NULL, created_at TEXT NOT NULL)`,
		`CREATE TABLE memberships (
			household_id TEXT NOT NULL, user_id TEXT NOT NULL, role TEXT NOT NULL, created_at TEXT NOT NULL,
			PRIMARY KEY (household_id, user_id))`,
		`CREATE TABLE sessions (
			token_hash TEXT PRIMARY KEY, user_id TEXT NOT NULL, active_household_id TEXT, expires_at TEXT NOT NULL, created_at TEXT NOT NULL)`,
		`CREATE TABLE utilities (
			id TEXT PRIMARY KEY, household_id TEXT NOT NULL, name TEXT NOT NULL, provider TEXT NOT NULL DEFAULT '', category TEXT NOT NULL, created_at TEXT NOT NULL)`,
		`CREATE TABLE bills (
			id TEXT PRIMARY KEY, household_id TEXT NOT NULL, utility_id TEXT NOT NULL,
			period_start TEXT NOT NULL, period_end TEXT NOT NULL, due_date TEXT NOT NULL,
			amount_cents INTEGER NOT NULL, paid_by_user_id TEXT, invoice_ref TEXT NOT NULL DEFAULT '',
			notes TEXT NOT NULL DEFAULT '', split_method TEXT NOT NULL, created_by TEXT NOT NULL, created_at TEXT NOT NULL)`,
		`CREATE TABLE bill_shares (
			bill_id TEXT NOT NULL, user_id TEXT NOT NULL, amount_cents INTEGER NOT NULL, weight INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (bill_id, user_id))`,
		`CREATE TABLE share_payments (
			id TEXT PRIMARY KEY, bill_id TEXT NOT NULL, user_id TEXT NOT NULL, amount_cents INTEGER NOT NULL,
			note TEXT NOT NULL DEFAULT '', recorded_by TEXT NOT NULL, created_at TEXT NOT NULL)`,
		`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`,
		`INSERT INTO schema_migrations (version, applied_at) VALUES (1, '2026-01-01T00:00:00Z')`,
		`INSERT INTO users (id, email, name, password_hash, created_at) VALUES ('userana', 'ana@example.com', 'Ana', 'hash', '2026-01-01T00:00:00Z')`,
		`INSERT INTO users (id, email, name, password_hash, created_at) VALUES ('userluis', 'luis@example.com', 'Luis', 'hash', '2026-01-01T00:00:00Z')`,
		`INSERT INTO households (id, name, currency, created_at) VALUES ('home', 'Home', 'USD', '2026-01-01T00:00:00Z')`,
		`INSERT INTO memberships (household_id, user_id, role, created_at) VALUES ('home', 'userana', 'owner', '2026-01-01T00:00:00Z')`,
		`INSERT INTO memberships (household_id, user_id, role, created_at) VALUES ('home', 'userluis', 'member', '2026-01-01T00:00:00Z')`,
		`INSERT INTO utilities (id, household_id, name, category, created_at) VALUES ('util', 'home', 'Electricity', 'electric', '2026-01-01T00:00:00Z')`,
		`INSERT INTO bills (id, household_id, utility_id, period_start, period_end, due_date, amount_cents, paid_by_user_id, split_method, created_by, created_at)
			VALUES ('bill', 'home', 'util', '2026-10-01', '2026-10-31', '2026-11-10', 10000, 'userana', 'equal', 'userana', '2026-01-02T00:00:00Z')`,
		`INSERT INTO bill_shares (bill_id, user_id, amount_cents, weight) VALUES ('bill', 'userana', 5000, 1)`,
		`INSERT INTO bill_shares (bill_id, user_id, amount_cents, weight) VALUES ('bill', 'userluis', 5000, 1)`,
		`INSERT INTO share_payments (id, bill_id, user_id, amount_cents, recorded_by, created_at) VALUES ('pay', 'bill', 'userluis', 5000, 'userana', '2026-01-03T00:00:00Z')`,
		`INSERT INTO sessions (token_hash, user_id, expires_at, created_at) VALUES ('sess', 'userana', '2026-02-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(stmt, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	bill, err := st.Bill(ctx, "home", "bill")
	if err != nil {
		t.Fatal(err)
	}
	if bill.AmountCents != 10000 || bill.PaidByName != "Ana" || len(bill.Shares) != 2 {
		t.Fatalf("bill %+v", bill)
	}
	var paid int64
	for _, sh := range bill.Shares {
		if sh.Name == "Luis" {
			paid = sh.PaidCents
		}
	}
	if paid != 5000 {
		t.Fatalf("luis paid %d", paid)
	}
	if bill.Status() != "settled" {
		t.Fatal(bill.Status())
	}
}
