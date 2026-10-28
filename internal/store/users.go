package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (s *Store) CreateUser(ctx context.Context, name, email, passwordHash string) (User, error) {
	id, err := newID()
	if err != nil {
		return User{}, err
	}
	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx, `INSERT INTO users (id, email, name, password_hash, created_at) VALUES (?, ?, ?, ?, ?)`,
		id, email, name, passwordHash, fmtTS(now))
	if isUnique(err) {
		return User{}, ErrConflict
	}
	if err != nil {
		return User{}, err
	}
	return User{ID: id, Email: email, Name: name, PasswordHash: passwordHash, CreatedAt: now}, nil
}

func (s *Store) UserByEmail(ctx context.Context, email string) (User, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, email, name, password_hash, created_at FROM users WHERE email = ?`, email)
	return scanUser(row)
}

func (s *Store) UserByID(ctx context.Context, id string) (User, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, email, name, password_hash, created_at FROM users WHERE id = ?`, id)
	return scanUser(row)
}

type scanner interface {
	Scan(dest ...any) error
}

func scanUser(row scanner) (User, error) {
	var u User
	var created string
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	u.CreatedAt, err = parseTS(created)
	if err != nil {
		return User{}, err
	}
	return u, nil
}
