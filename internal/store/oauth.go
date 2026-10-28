package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"hearth/internal/token"
)

func (s *Store) CreateAuthCode(ctx context.Context, clientID, userID, redirectURI, invite string) (string, error) {
	raw, err := token.New()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	_, _ = s.db.ExecContext(ctx, `DELETE FROM oauth_codes WHERE expires_at < ?`, fmtTS(now))
	_, err = s.db.ExecContext(ctx, `INSERT INTO oauth_codes (code_hash, client_id, user_id, redirect_uri, invite_token, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		hashToken(raw), clientID, userID, redirectURI, invite, fmtTS(now.Add(time.Minute)), fmtTS(now))
	if err != nil {
		return "", err
	}
	return raw, nil
}

func (s *Store) ConsumeAuthCode(ctx context.Context, raw, clientID, redirectURI string) (userID, invite string, err error) {
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		var expStr, gotClient, gotRedirect string
		qerr := tx.QueryRowContext(ctx, `SELECT user_id, client_id, redirect_uri, invite_token, expires_at FROM oauth_codes WHERE code_hash = ?`, hashToken(raw)).
			Scan(&userID, &gotClient, &gotRedirect, &invite, &expStr)
		if errors.Is(qerr, sql.ErrNoRows) {
			return ErrNotFound
		}
		if qerr != nil {
			return qerr
		}
		if _, qerr = tx.ExecContext(ctx, `DELETE FROM oauth_codes WHERE code_hash = ?`, hashToken(raw)); qerr != nil {
			return qerr
		}
		exp, qerr := parseTS(expStr)
		if qerr != nil {
			return qerr
		}
		if time.Now().After(exp) {
			return ErrExpired
		}
		if gotClient != clientID || gotRedirect != redirectURI {
			return ErrInvalid
		}
		return nil
	})
	if err != nil {
		return "", "", err
	}
	return userID, invite, nil
}

func (s *Store) CreateRefresh(ctx context.Context, clientID, userID, householdID string, ttl time.Duration) (string, error) {
	raw, _, err := s.insertRefresh(ctx, "", clientID, userID, householdID, ttl)
	return raw, err
}

func (s *Store) insertRefresh(ctx context.Context, family, clientID, userID, householdID string, ttl time.Duration) (string, string, error) {
	raw, err := token.New()
	if err != nil {
		return "", "", err
	}
	if family == "" {
		family, err = token.New()
		if err != nil {
			return "", "", err
		}
	}
	now := time.Now().UTC()
	var household any
	if householdID != "" {
		household = householdID
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO oauth_refresh_tokens (token_hash, family_id, client_id, user_id, household_id, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		hashToken(raw), family, clientID, userID, household, fmtTS(now.Add(ttl)), fmtTS(now))
	if err != nil {
		return "", "", err
	}
	return raw, family, nil
}

func (s *Store) RotateRefresh(ctx context.Context, raw, clientID string, ttl time.Duration) (userID, householdID, newRaw string, err error) {
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		var family, gotClient, expStr string
		var household, revoked sql.NullString
		qerr := tx.QueryRowContext(ctx, `SELECT family_id, client_id, user_id, household_id, expires_at, revoked_at FROM oauth_refresh_tokens WHERE token_hash = ?`, hashToken(raw)).
			Scan(&family, &gotClient, &userID, &household, &expStr, &revoked)
		if errors.Is(qerr, sql.ErrNoRows) {
			return ErrNotFound
		}
		if qerr != nil {
			return qerr
		}
		if revoked.Valid {
			if _, qerr = tx.ExecContext(ctx, `UPDATE oauth_refresh_tokens SET revoked_at = ? WHERE family_id = ? AND revoked_at IS NULL`, fmtTS(time.Now().UTC()), family); qerr != nil {
				return qerr
			}
			return ErrRevoked
		}
		if gotClient != clientID {
			return ErrInvalid
		}
		exp, qerr := parseTS(expStr)
		if qerr != nil {
			return qerr
		}
		if time.Now().After(exp) {
			return ErrExpired
		}
		now := time.Now().UTC()
		if _, qerr = tx.ExecContext(ctx, `UPDATE oauth_refresh_tokens SET revoked_at = ? WHERE token_hash = ?`, fmtTS(now), hashToken(raw)); qerr != nil {
			return qerr
		}
		next, err := token.New()
		if err != nil {
			return err
		}
		var householdVal any
		if household.Valid {
			householdID = household.String
			householdVal = household.String
		}
		if _, qerr = tx.ExecContext(ctx, `INSERT INTO oauth_refresh_tokens (token_hash, family_id, client_id, user_id, household_id, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			hashToken(next), family, clientID, userID, householdVal, fmtTS(now.Add(ttl)), fmtTS(now)); qerr != nil {
			return qerr
		}
		newRaw = next
		return nil
	})
	if err != nil {
		return "", "", "", err
	}
	return userID, householdID, newRaw, nil
}

func (s *Store) RevokeRefresh(ctx context.Context, raw string) error {
	var family string
	err := s.db.QueryRowContext(ctx, `SELECT family_id FROM oauth_refresh_tokens WHERE token_hash = ?`, hashToken(raw)).Scan(&family)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE oauth_refresh_tokens SET revoked_at = ? WHERE family_id = ? AND revoked_at IS NULL`, fmtTS(time.Now().UTC()), family)
	return err
}

func (s *Store) SetRefreshHousehold(ctx context.Context, raw, householdID string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE oauth_refresh_tokens SET household_id = ? WHERE token_hash = ? AND revoked_at IS NULL`, householdID, hashToken(raw))
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
