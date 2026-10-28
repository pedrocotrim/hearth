package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"hearth/internal/billing"
)

const billSelect = `
SELECT
    b.id, b.household_id, b.utility_id, u.name, u.provider, u.category,
    b.period_start, b.period_end, b.due_date, b.amount_cents,
    b.paid_by_participant_id, COALESCE(payer.name, ''),
    b.invoice_ref, b.notes, b.split_method, b.created_by, b.created_at
FROM bills b
JOIN utilities u ON u.id = b.utility_id
LEFT JOIN participants payer ON payer.id = b.paid_by_participant_id`

func (s *Store) CreateUtility(ctx context.Context, householdID, name, provider, category string) (Utility, error) {
	id, err := newID()
	if err != nil {
		return Utility{}, err
	}
	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx, `INSERT INTO utilities (id, household_id, name, provider, category, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, householdID, name, provider, category, fmtTS(now))
	if err != nil {
		return Utility{}, err
	}
	return Utility{ID: id, HouseholdID: householdID, Name: name, Provider: provider, Category: category, CreatedAt: now}, nil
}

func (s *Store) Utilities(ctx context.Context, householdID string) ([]Utility, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, household_id, name, provider, category, created_at FROM utilities WHERE household_id = ? ORDER BY name COLLATE NOCASE`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Utility
	for rows.Next() {
		var u Utility
		var created string
		if err := rows.Scan(&u.ID, &u.HouseholdID, &u.Name, &u.Provider, &u.Category, &created); err != nil {
			return nil, err
		}
		u.CreatedAt, err = parseTS(created)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) SetExemptions(ctx context.Context, householdID, utilityID string, participantIDs []string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var owner string
		err := tx.QueryRowContext(ctx, `SELECT household_id FROM utilities WHERE id = ?`, utilityID).Scan(&owner)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && owner != householdID) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM utility_exemptions WHERE utility_id = ?`, utilityID); err != nil {
			return err
		}
		for _, id := range participantIDs {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM participants WHERE id = ? AND household_id = ? AND active = 1`, id, householdID).Scan(&n); err != nil {
				return err
			}
			if n != 1 {
				return ErrInvalid
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO utility_exemptions (utility_id, participant_id) VALUES (?, ?)`, utilityID, id); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) ExemptionSet(ctx context.Context, householdID, utilityID string) (map[string]struct{}, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT e.participant_id
		FROM utility_exemptions e
		JOIN utilities u ON u.id = e.utility_id
		WHERE e.utility_id = ? AND u.household_id = ?`, utilityID, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]struct{}{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = struct{}{}
	}
	return out, rows.Err()
}

func (s *Store) CreateBill(ctx context.Context, in NewBill) (string, error) {
	if in.AmountCents <= 0 {
		return "", ErrInvalid
	}
	var sum int64
	seen := map[string]bool{}
	for _, sh := range in.Shares {
		if sh.ParticipantID == "" || seen[sh.ParticipantID] || sh.AmountCents < 0 {
			return "", ErrInvalid
		}
		seen[sh.ParticipantID] = true
		sum += sh.AmountCents
	}
	if sum != in.AmountCents {
		return "", ErrInvalid
	}
	if in.PaidByParticipantID != "" && !seen[in.PaidByParticipantID] {
		return "", ErrInvalid
	}
	id, err := newID()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		var owner string
		err := tx.QueryRowContext(ctx, `SELECT household_id FROM utilities WHERE id = ?`, in.UtilityID).Scan(&owner)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && owner != in.HouseholdID) {
			return ErrInvalid
		}
		if err != nil {
			return err
		}
		for _, sh := range in.Shares {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM participants WHERE household_id = ? AND id = ? AND active = 1`, in.HouseholdID, sh.ParticipantID).Scan(&n); err != nil {
				return err
			}
			if n != 1 {
				return ErrInvalid
			}
		}
		var payer any
		if in.PaidByParticipantID != "" {
			payer = in.PaidByParticipantID
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO bills (
			id, household_id, utility_id, period_start, period_end, due_date, amount_cents,
			paid_by_participant_id, invoice_ref, notes, split_method, created_by, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, in.HouseholdID, in.UtilityID, fmtDate(in.PeriodStart), fmtDate(in.PeriodEnd), fmtDate(in.DueDate), in.AmountCents,
			payer, in.InvoiceRef, in.Notes, in.SplitMethod, in.CreatedBy, fmtTS(now))
		if err != nil {
			return err
		}
		for _, sh := range in.Shares {
			paid := 0
			if sh.Paid {
				paid = 1
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO bill_shares (bill_id, participant_id, amount_cents, weight, paid) VALUES (?, ?, ?, ?, ?)`,
				id, sh.ParticipantID, sh.AmountCents, sh.Weight, paid); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

func (s *Store) Bills(ctx context.Context, householdID string) ([]Bill, error) {
	return s.queryBills(ctx, s.db, `WHERE b.household_id = ? ORDER BY b.due_date DESC, b.created_at DESC`, householdID)
}

func (s *Store) Bill(ctx context.Context, householdID, billID string) (Bill, error) {
	bills, err := s.queryBills(ctx, s.db, `WHERE b.household_id = ? AND b.id = ?`, householdID, billID)
	if err != nil {
		return Bill{}, err
	}
	if len(bills) == 0 {
		return Bill{}, ErrNotFound
	}
	return bills[0], nil
}

func (s *Store) DeleteBill(ctx context.Context, householdID, billID string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		bills, err := s.queryBills(ctx, tx, `WHERE b.household_id = ? AND b.id = ?`, householdID, billID)
		if err != nil {
			return err
		}
		if len(bills) == 0 {
			return ErrNotFound
		}
		for _, sh := range bills[0].Shares {
			if sh.PaidCents > 0 {
				return ErrHasPayments
			}
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM bills WHERE id = ? AND household_id = ?`, billID, householdID)
		return err
	})
}

func (s *Store) MarkProviderPaid(ctx context.Context, householdID, billID, payerID string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		bills, err := s.queryBills(ctx, tx, `WHERE b.household_id = ? AND b.id = ?`, householdID, billID)
		if err != nil {
			return err
		}
		if len(bills) == 0 {
			return ErrNotFound
		}
		bill := bills[0]
		if bill.PaidByParticipantID != "" {
			return ErrInvalid
		}
		found := false
		for _, sh := range bill.Shares {
			if sh.ParticipantID == payerID {
				found = true
				break
			}
		}
		if !found {
			return ErrInvalid
		}
		res, err := tx.ExecContext(ctx, `UPDATE bills SET paid_by_participant_id = ? WHERE id = ? AND household_id = ? AND paid_by_participant_id IS NULL`, payerID, billID, householdID)
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
	})
}

func (s *Store) AddPayment(ctx context.Context, householdID, billID, userID string, amount int64, note, recordedBy string) error {
	if amount <= 0 {
		return ErrInvalid
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		bills, err := s.queryBills(ctx, tx, `WHERE b.household_id = ? AND b.id = ?`, householdID, billID)
		if err != nil {
			return err
		}
		if len(bills) == 0 {
			return ErrNotFound
		}
		bill := bills[0]
		if bill.PaidByParticipantID == "" {
			return ErrProviderUnpaid
		}
		if userID == bill.PaidByParticipantID {
			return ErrInvalid
		}
		var share *Share
		for i := range bill.Shares {
			if bill.Shares[i].ParticipantID == userID {
				share = &bill.Shares[i]
				break
			}
		}
		if share == nil {
			return ErrNotFound
		}
		if amount > share.AmountCents-share.PaidCents {
			return ErrPaymentTooLarge
		}
		id, err := newID()
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO share_payments (id, bill_id, participant_id, amount_cents, note, recorded_by, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			id, billID, userID, amount, note, recordedBy, fmtTS(time.Now().UTC()))
		return err
	})
}

func (b Bill) Status() string {
	states := make([]billing.ShareState, len(b.Shares))
	for i, sh := range b.Shares {
		states[i] = billing.ShareState{UserID: sh.ParticipantID, AmountCents: sh.AmountCents, PaidCents: settledCents(sh)}
	}
	return billing.DeriveStatus(b.PaidByParticipantID, states)
}

func (b Bill) Debts() []billing.Debt {
	states := make([]billing.ShareState, len(b.Shares))
	for i, sh := range b.Shares {
		states[i] = billing.ShareState{UserID: sh.ParticipantID, AmountCents: sh.AmountCents, PaidCents: settledCents(sh)}
	}
	return billing.Debts(b.PaidByParticipantID, states)
}

func settledCents(sh Share) int64 {
	if sh.Paid {
		return sh.AmountCents
	}
	return 0
}

func (s *Store) SetSharePaid(ctx context.Context, householdID, billID, participantID string, paid bool) error {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM bill_shares s
		JOIN bills b ON b.id = s.bill_id
		WHERE s.bill_id = ? AND s.participant_id = ? AND b.household_id = ?`, billID, participantID, householdID).Scan(&n)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	flag := 0
	if paid {
		flag = 1
	}
	_, err = s.db.ExecContext(ctx, `UPDATE bill_shares SET paid = ? WHERE bill_id = ? AND participant_id = ?`, flag, billID, participantID)
	return err
}

func (s *Store) queryBills(ctx context.Context, q querier, clause string, args ...any) ([]Bill, error) {
	rows, err := q.QueryContext(ctx, billSelect+" "+clause, args...)
	if err != nil {
		return nil, err
	}
	bills, err := readBills(rows)
	if err != nil {
		return nil, err
	}
	if len(bills) == 0 {
		return bills, nil
	}
	if err := attachShares(ctx, q, bills); err != nil {
		return nil, err
	}
	return bills, nil
}

func readBills(rows *sql.Rows) ([]Bill, error) {
	defer rows.Close()
	var out []Bill
	for rows.Next() {
		var b Bill
		var start, end, due, created string
		var payer sql.NullString
		if err := rows.Scan(
			&b.ID, &b.HouseholdID, &b.UtilityID, &b.UtilityName, &b.Provider, &b.Category,
			&start, &end, &due, &b.AmountCents,
			&payer, &b.PaidByName,
			&b.InvoiceRef, &b.Notes, &b.SplitMethod, &b.CreatedBy, &created,
		); err != nil {
			return nil, err
		}
		var err error
		if b.PeriodStart, err = parseDate(start); err != nil {
			return nil, err
		}
		if b.PeriodEnd, err = parseDate(end); err != nil {
			return nil, err
		}
		if b.DueDate, err = parseDate(due); err != nil {
			return nil, err
		}
		if b.CreatedAt, err = parseTS(created); err != nil {
			return nil, err
		}
		if payer.Valid {
			b.PaidByParticipantID = payer.String
		}
		b.Shares = []Share{}
		out = append(out, b)
	}
	return out, rows.Err()
}

func attachShares(ctx context.Context, q querier, bills []Bill) error {
	args := make([]any, len(bills))
	idx := make(map[string]int, len(bills))
	for i, b := range bills {
		args[i] = b.ID
		idx[b.ID] = i
	}
	in := placeholders(len(bills))
	rows, err := q.QueryContext(ctx, `
		SELECT s.bill_id, s.participant_id, p.name, s.amount_cents, s.weight, s.paid
		FROM bill_shares s
		JOIN participants p ON p.id = s.participant_id
		WHERE s.bill_id IN (`+in+`)
		ORDER BY p.name COLLATE NOCASE`, args...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var billID string
		var sh Share
		var paid int
		if err := rows.Scan(&billID, &sh.ParticipantID, &sh.Name, &sh.AmountCents, &sh.Weight, &paid); err != nil {
			rows.Close()
			return err
		}
		i, ok := idx[billID]
		if !ok {
			continue
		}
		sh.Paid = paid == 1
		bills[i].Shares = append(bills[i].Shares, sh)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	payRows, err := q.QueryContext(ctx, `
		SELECT bill_id, participant_id, COALESCE(SUM(amount_cents), 0)
		FROM share_payments
		WHERE bill_id IN (`+in+`)
		GROUP BY bill_id, participant_id`, args...)
	if err != nil {
		return err
	}
	defer payRows.Close()
	for payRows.Next() {
		var billID, userID string
		var paid int64
		if err := payRows.Scan(&billID, &userID, &paid); err != nil {
			return err
		}
		i, ok := idx[billID]
		if !ok {
			continue
		}
		for j := range bills[i].Shares {
			if bills[i].Shares[j].ParticipantID == userID {
				bills[i].Shares[j].PaidCents = paid
			}
		}
	}
	return payRows.Err()
}
