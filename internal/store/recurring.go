package store

import (
	"context"
	"time"
)

func (s *Store) SetRepeats(ctx context.Context, householdID, utilityID string, repeats bool) error {
	flag := 0
	if repeats {
		flag = 1
	}
	res, err := s.db.ExecContext(ctx, `UPDATE utilities SET repeats = ? WHERE id = ? AND household_id = ?`, flag, utilityID, householdID)
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

// EnsureRecurring copies the latest bill of each repeating utility into each
// month whose period has started. A utility is skipped when someone on that
// bill is no longer active, so the copy cannot change who shares it.
func (s *Store) EnsureRecurring(ctx context.Context, householdID string, today time.Time) error {
	utils, err := s.Utilities(ctx, householdID)
	if err != nil {
		return err
	}
	for _, u := range utils {
		if !u.Repeats {
			continue
		}
		if err := s.repeatUtility(ctx, householdID, u.ID, today); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) repeatUtility(ctx context.Context, householdID, utilityID string, today time.Time) error {
	latest, err := s.latestBill(ctx, householdID, utilityID)
	if err != nil || latest.ID == "" {
		return err
	}
	for step := 0; step < 24; step++ {
		start, end, due := addBillMonth(latest.PeriodStart, latest.PeriodEnd, latest.DueDate)
		if calendarDate(start) > calendarDate(today) {
			return nil
		}
		existing, found, err := s.billOnPeriod(ctx, householdID, utilityID, start)
		if err != nil {
			return err
		}
		if found {
			latest = existing
			continue
		}
		for _, sh := range latest.Shares {
			ok, err := activeParticipant(ctx, s.db, householdID, sh.ParticipantID)
			if err != nil {
				return err
			}
			if !ok {
				return nil
			}
		}
		shares := make([]NewShare, len(latest.Shares))
		for i, sh := range latest.Shares {
			shares[i] = NewShare{ParticipantID: sh.ParticipantID, AmountCents: sh.AmountCents, Weight: sh.Weight}
		}
		id, err := s.CreateBill(ctx, NewBill{
			HouseholdID: householdID,
			UtilityID:   utilityID,
			PeriodStart: start,
			PeriodEnd:   end,
			DueDate:     due,
			AmountCents: latest.AmountCents,
			Notes:       latest.Notes,
			SplitMethod: latest.SplitMethod,
			CreatedBy:   latest.CreatedBy,
			Shares:      shares,
		})
		if err != nil {
			return err
		}
		latest, err = s.Bill(ctx, householdID, id)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) latestBill(ctx context.Context, householdID, utilityID string) (Bill, error) {
	bills, err := s.queryBills(ctx, s.db, `WHERE b.household_id = ? AND b.utility_id = ? ORDER BY b.period_start DESC, b.created_at DESC LIMIT 1`, householdID, utilityID)
	if err != nil || len(bills) == 0 {
		return Bill{}, err
	}
	return bills[0], nil
}

func (s *Store) billOnPeriod(ctx context.Context, householdID, utilityID string, start time.Time) (Bill, bool, error) {
	bills, err := s.queryBills(ctx, s.db, `WHERE b.household_id = ? AND b.utility_id = ? AND b.period_start = ?`, householdID, utilityID, fmtDate(start))
	if err != nil || len(bills) == 0 {
		return Bill{}, false, err
	}
	return bills[0], true, nil
}

func addBillMonth(start, end, due time.Time) (time.Time, time.Time, time.Time) {
	start = dateOnly(start)
	end = dateOnly(end)
	due = dateOnly(due)
	nextEnd := end.AddDate(0, 1, 0)
	if monthEnd(end) {
		y, m, _ := end.Date()
		nextEnd = time.Date(y, m+1, 1, 0, 0, 0, 0, end.Location()).AddDate(0, 1, -1)
	}
	return start.AddDate(0, 1, 0), nextEnd, due.AddDate(0, 1, 0)
}

func monthEnd(t time.Time) bool {
	t = dateOnly(t)
	return t.AddDate(0, 0, 1).Month() != t.Month()
}

func dateOnly(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

func calendarDate(t time.Time) string {
	return t.Format("2006-01-02")
}
