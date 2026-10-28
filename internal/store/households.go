package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"hearth/internal/billing"
	"hearth/internal/token"
)

func (s *Store) CreateHousehold(ctx context.Context, name, currency, ownerID string) (Household, error) {
	id, err := newID()
	if err != nil {
		return Household{}, err
	}
	now := time.Now().UTC()
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		var ownerName, ownerEmail string
		if err := tx.QueryRowContext(ctx, `SELECT name, email FROM users WHERE id = ?`, ownerID).Scan(&ownerName, &ownerEmail); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO households (id, name, currency, created_at) VALUES (?, ?, ?, ?)`, id, name, currency, fmtTS(now)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO memberships (household_id, user_id, role, created_at) VALUES (?, ?, 'owner', ?)`, id, ownerID, fmtTS(now)); err != nil {
			return err
		}
		pid, err := newID()
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO participants (id, household_id, name, email, user_id, active, created_at) VALUES (?, ?, ?, ?, ?, 1, ?)`,
			pid, id, ownerName, ownerEmail, ownerID, fmtTS(now))
		return err
	})
	if err != nil {
		return Household{}, err
	}
	return Household{ID: id, Name: name, Currency: currency, CreatedAt: now}, nil
}

func (s *Store) ListHouseholds(ctx context.Context, userID string) ([]Household, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT h.id, h.name, h.currency, h.created_at
		FROM households h
		JOIN memberships m ON m.household_id = h.id
		WHERE m.user_id = ?
		ORDER BY h.created_at ASC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Household
	for rows.Next() {
		var h Household
		var created string
		if err := rows.Scan(&h.ID, &h.Name, &h.Currency, &created); err != nil {
			return nil, err
		}
		h.CreatedAt, err = parseTS(created)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *Store) Membership(ctx context.Context, householdID, userID string) (Membership, error) {
	var m Membership
	var created string
	err := s.db.QueryRowContext(ctx, `SELECT household_id, user_id, role, created_at FROM memberships WHERE household_id = ? AND user_id = ?`, householdID, userID).
		Scan(&m.HouseholdID, &m.UserID, &m.Role, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return Membership{}, ErrNotFound
	}
	if err != nil {
		return Membership{}, err
	}
	m.CreatedAt, err = parseTS(created)
	return m, err
}

func (s *Store) Members(ctx context.Context, householdID string) ([]Member, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.id, COALESCE(p.user_id, ''), p.name, COALESCE(p.email, ''), COALESCE(m.role, ''), p.created_at
		FROM participants p
		LEFT JOIN memberships m ON m.household_id = p.household_id AND m.user_id = p.user_id
		WHERE p.household_id = ? AND p.active = 1
		ORDER BY CASE COALESCE(m.role, '') WHEN 'owner' THEN 0 WHEN 'member' THEN 1 ELSE 2 END, p.name COLLATE NOCASE`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var m Member
		var joined string
		if err := rows.Scan(&m.ParticipantID, &m.UserID, &m.Name, &m.Email, &m.Role, &joined); err != nil {
			return nil, err
		}
		m.Joined, err = parseTS(joined)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) ParticipantForUser(ctx context.Context, householdID, userID string) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM participants WHERE household_id = ? AND user_id = ? AND active = 1`, householdID, userID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return id, nil
}

func (s *Store) RenameHousehold(ctx context.Context, id, name string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE households SET name = ? WHERE id = ?`, name, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) HasMemberEmail(ctx context.Context, householdID, email string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM memberships m
		JOIN users u ON u.id = m.user_id
		WHERE m.household_id = ? AND u.email = ?`, householdID, email).Scan(&n)
	return n > 0, err
}

func (s *Store) AddParticipant(ctx context.Context, householdID, name, email string) (string, error) {
	id, err := newID()
	if err != nil {
		return "", err
	}
	var emailVal any
	if email != "" {
		emailVal = email
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO participants (id, household_id, name, email, active, created_at) VALUES (?, ?, ?, ?, 1, ?)`,
		id, householdID, name, emailVal, fmtTS(time.Now().UTC()))
	if isUnique(err) {
		return "", ErrConflict
	}
	if err != nil {
		return "", err
	}
	return id, nil
}

func (s *Store) RemoveParticipant(ctx context.Context, householdID, participantID string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var userID sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT user_id FROM participants WHERE id = ? AND household_id = ? AND active = 1`, participantID, householdID).Scan(&userID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if userID.Valid {
			var role string
			err = tx.QueryRowContext(ctx, `SELECT role FROM memberships WHERE household_id = ? AND user_id = ?`, householdID, userID.String).Scan(&role)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if role == "owner" {
				var owners int
				if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM memberships WHERE household_id = ? AND role = 'owner'`, householdID).Scan(&owners); err != nil {
					return err
				}
				if owners <= 1 {
					return ErrLastOwner
				}
			}
		}
		bills, err := s.queryBills(ctx, tx, `WHERE b.household_id = ?`, householdID)
		if err != nil {
			return err
		}
		for _, bill := range bills {
			onBill := false
			for _, sh := range bill.Shares {
				if sh.ParticipantID == participantID {
					onBill = true
					break
				}
			}
			if onBill && bill.Status() != billing.StatusSettled {
				return ErrOpenBills
			}
		}
		if userID.Valid {
			if _, err := tx.ExecContext(ctx, `DELETE FROM memberships WHERE household_id = ? AND user_id = ?`, householdID, userID.String); err != nil {
				return err
			}
		}
		var shares int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM bill_shares WHERE participant_id = ?`, participantID).Scan(&shares); err != nil {
			return err
		}
		if shares == 0 {
			_, err = tx.ExecContext(ctx, `DELETE FROM participants WHERE id = ? AND household_id = ?`, participantID, householdID)
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE participants SET active = 0, user_id = NULL WHERE id = ? AND household_id = ?`, participantID, householdID)
		return err
	})
}

func (s *Store) CreateInvite(ctx context.Context, householdID, email, createdBy string, ttl time.Duration) (string, error) {
	raw, err := token.New()
	if err != nil {
		return "", err
	}
	id, err := newID()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM invites WHERE household_id = ? AND email = ? AND accepted_at IS NULL`, householdID, email); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO invites (id, household_id, email, token_hash, created_by, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			id, householdID, email, hashToken(raw), createdBy, fmtTS(now.Add(ttl)), fmtTS(now))
		return err
	})
	if err != nil {
		return "", err
	}
	return raw, nil
}

func (s *Store) PendingInvites(ctx context.Context, householdID string) ([]Invite, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT i.id, i.household_id, h.name, i.email, i.expires_at, i.accepted_at, i.created_at
		FROM invites i
		JOIN households h ON h.id = i.household_id
		WHERE i.household_id = ? AND i.accepted_at IS NULL AND i.expires_at > ?
		ORDER BY i.created_at DESC`, householdID, fmtTS(time.Now().UTC()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Invite
	for rows.Next() {
		inv, err := scanInvite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

func (s *Store) InviteByToken(ctx context.Context, raw string) (Invite, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT i.id, i.household_id, h.name, i.email, i.expires_at, i.accepted_at, i.created_at
		FROM invites i
		JOIN households h ON h.id = i.household_id
		WHERE i.token_hash = ?`, hashToken(raw))
	inv, err := scanInvite(row)
	if err != nil {
		return Invite{}, err
	}
	if inv.AcceptedAt != nil {
		return Invite{}, ErrUsed
	}
	if time.Now().After(inv.ExpiresAt) {
		return Invite{}, ErrExpired
	}
	return inv, nil
}

func (s *Store) RevokeInvite(ctx context.Context, householdID, inviteID string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM invites WHERE id = ? AND household_id = ? AND accepted_at IS NULL`, inviteID, householdID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) AcceptInvite(ctx context.Context, raw, userID, email string) (string, error) {
	var householdID string
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var id, hid, invEmail, expStr string
		var accepted sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT id, household_id, email, expires_at, accepted_at FROM invites WHERE token_hash = ?`, hashToken(raw)).
			Scan(&id, &hid, &invEmail, &expStr, &accepted)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if accepted.Valid {
			return ErrUsed
		}
		exp, err := parseTS(expStr)
		if err != nil {
			return err
		}
		if time.Now().After(exp) {
			return ErrExpired
		}
		if !strings.EqualFold(invEmail, email) {
			return ErrEmailMismatch
		}
		now := fmtTS(time.Now().UTC())
		if _, err := tx.ExecContext(ctx, `INSERT INTO memberships (household_id, user_id, role, created_at) VALUES (?, ?, 'member', ?) ON CONFLICT (household_id, user_id) DO NOTHING`, hid, userID, now); err != nil {
			return err
		}
		var userName string
		if err := tx.QueryRowContext(ctx, `SELECT name FROM users WHERE id = ?`, userID).Scan(&userName); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE participants SET user_id = ?, name = ? WHERE household_id = ? AND email = ? AND user_id IS NULL AND active = 1`, userID, userName, hid, invEmail)
		if err != nil {
			return err
		}
		linked, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if linked == 0 {
			var existing int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM participants WHERE household_id = ? AND user_id = ?`, hid, userID).Scan(&existing); err != nil {
				return err
			}
			if existing == 0 {
				pid, err := newID()
				if err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO participants (id, household_id, name, email, user_id, active, created_at) VALUES (?, ?, ?, ?, ?, 1, ?)`,
					pid, hid, userName, invEmail, userID, now); err != nil {
					return err
				}
			}
		}
		res, err = tx.ExecContext(ctx, `UPDATE invites SET accepted_at = ? WHERE id = ? AND accepted_at IS NULL`, now, id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrUsed
		}
		householdID = hid
		return nil
	})
	return householdID, err
}

func scanInvite(row scanner) (Invite, error) {
	var inv Invite
	var exp, created string
	var accepted sql.NullString
	err := row.Scan(&inv.ID, &inv.HouseholdID, &inv.HouseholdName, &inv.Email, &exp, &accepted, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return Invite{}, ErrNotFound
	}
	if err != nil {
		return Invite{}, err
	}
	inv.ExpiresAt, err = parseTS(exp)
	if err != nil {
		return Invite{}, err
	}
	inv.CreatedAt, err = parseTS(created)
	if err != nil {
		return Invite{}, err
	}
	if accepted.Valid {
		t, err := parseTS(accepted.String)
		if err != nil {
			return Invite{}, err
		}
		inv.AcceptedAt = &t
	}
	return inv, nil
}
