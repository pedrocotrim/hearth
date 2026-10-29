package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "hearth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestPayerOutsideTheSplit(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	ana, home, anaPart := testHousehold(t, st)
	kid := testParticipant(t, st, home, "Kid")
	payer := testParticipant(t, st, home, "Payer")
	util := testUtility(t, st, home)

	id, err := st.CreateBill(ctx, NewBill{
		HouseholdID:         home,
		UtilityID:           util,
		PeriodStart:         date(2026, 1, 1),
		PeriodEnd:           date(2026, 1, 31),
		DueDate:             date(2026, 2, 10),
		AmountCents:         10000,
		PaidByParticipantID: payer,
		SplitMethod:         "equal",
		CreatedBy:           ana,
		Shares:              []NewShare{{ParticipantID: anaPart, AmountCents: 4000}, {ParticipantID: kid, AmountCents: 6000}},
	})
	if err != nil {
		t.Fatal(err)
	}
	bill, err := st.Bill(ctx, home, id)
	if err != nil {
		t.Fatal(err)
	}
	debts := bill.Debts()
	if len(debts) != 2 {
		t.Fatalf("debts %+v", debts)
	}
	owed := map[string]int64{}
	for _, d := range debts {
		if d.ToID != payer {
			t.Fatalf("debt to %s", d.ToID)
		}
		owed[d.FromID] = d.Cents
	}
	if owed[anaPart] != 4000 || owed[kid] != 6000 {
		t.Fatalf("owed %+v", owed)
	}

	openID, err := st.CreateBill(ctx, NewBill{
		HouseholdID: home,
		UtilityID:   util,
		PeriodStart: date(2026, 2, 1),
		PeriodEnd:   date(2026, 2, 28),
		DueDate:     date(2026, 3, 10),
		AmountCents: 5000,
		SplitMethod: "equal",
		CreatedBy:   ana,
		Shares:      []NewShare{{ParticipantID: kid, AmountCents: 5000}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.MarkProviderPaid(ctx, home, openID, payer); err != nil {
		t.Fatal(err)
	}
	marked, err := st.Bill(ctx, home, openID)
	if err != nil {
		t.Fatal(err)
	}
	debts = marked.Debts()
	if len(debts) != 1 || debts[0].FromID != kid || debts[0].ToID != payer || debts[0].Cents != 5000 {
		t.Fatalf("marked debts %+v", debts)
	}
}

func TestUpdateBillKeepsUnchangedPaidFlags(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	ana, home, anaPart := testHousehold(t, st)
	kid := testParticipant(t, st, home, "Kid")
	payer := testParticipant(t, st, home, "Payer")
	util := testUtility(t, st, home)
	id, err := st.CreateBill(ctx, NewBill{
		HouseholdID:         home,
		UtilityID:           util,
		PeriodStart:         date(2026, 1, 1),
		PeriodEnd:           date(2026, 1, 31),
		DueDate:             date(2026, 2, 10),
		AmountCents:         10000,
		PaidByParticipantID: payer,
		InvoiceRef:          "old",
		Notes:               "note",
		SplitMethod:         "amounts",
		CreatedBy:           ana,
		Shares: []NewShare{
			{ParticipantID: anaPart, AmountCents: 4000, Weight: 1, Paid: true},
			{ParticipantID: kid, AmountCents: 6000, Weight: 2, Paid: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddPayment(ctx, home, id, anaPart, 4000, "", ana); err != nil {
		t.Fatal(err)
	}
	if err := st.AddPayment(ctx, home, id, kid, 6000, "", ana); err != nil {
		t.Fatal(err)
	}
	err = st.UpdateBill(ctx, home, id, NewBill{
		HouseholdID:         home,
		UtilityID:           util,
		PeriodStart:         date(2026, 1, 1),
		PeriodEnd:           date(2026, 1, 31),
		DueDate:             date(2026, 2, 12),
		AmountCents:         11000,
		PaidByParticipantID: payer,
		InvoiceRef:          "new",
		Notes:               "edited",
		SplitMethod:         "amounts",
		Shares: []NewShare{
			{ParticipantID: anaPart, AmountCents: 4000, Weight: 1},
			{ParticipantID: kid, AmountCents: 7000, Weight: 3},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	bill, err := st.Bill(ctx, home, id)
	if err != nil {
		t.Fatal(err)
	}
	if bill.AmountCents != 11000 || bill.InvoiceRef != "new" || bill.Notes != "edited" || bill.DueDate.Day() != 12 {
		t.Fatalf("bill %+v", bill)
	}
	for _, sh := range bill.Shares {
		switch sh.ParticipantID {
		case anaPart:
			if !sh.Paid || sh.PaidCents != 4000 || sh.AmountCents != 4000 {
				t.Fatalf("ana share %+v", sh)
			}
		case kid:
			if sh.Paid || sh.PaidCents != 0 || sh.AmountCents != 7000 {
				t.Fatalf("kid share %+v", sh)
			}
		default:
			t.Fatalf("unexpected share %+v", sh)
		}
	}
}

func TestEnsureRecurringCopiesMissedMonths(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	ana, home, anaPart := testHousehold(t, st)
	kid := testParticipant(t, st, home, "Kid")
	util := testUtility(t, st, home)
	if err := st.SetRepeats(ctx, home, util, true); err != nil {
		t.Fatal(err)
	}
	_, err := st.CreateBill(ctx, NewBill{
		HouseholdID:         home,
		UtilityID:           util,
		PeriodStart:         date(2026, 1, 1),
		PeriodEnd:           date(2026, 1, 31),
		DueDate:             date(2026, 2, 10),
		AmountCents:         10000,
		PaidByParticipantID: anaPart,
		InvoiceRef:          "INV-1",
		Notes:               "meter",
		SplitMethod:         "weights",
		CreatedBy:           ana,
		Shares: []NewShare{
			{ParticipantID: anaPart, AmountCents: 4000, Weight: 2, Paid: true},
			{ParticipantID: kid, AmountCents: 6000, Weight: 3, Paid: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	today := date(2026, 3, 15)
	if err := st.EnsureRecurring(ctx, home, today); err != nil {
		t.Fatal(err)
	}
	bills, err := st.Bills(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	if len(bills) != 3 {
		t.Fatalf("bills %d", len(bills))
	}
	byStart := map[string]Bill{}
	for _, b := range bills {
		byStart[b.PeriodStart.Format("2006-01-02")] = b
	}
	feb, ok := byStart["2026-02-01"]
	mar, okMar := byStart["2026-03-01"]
	if !ok || !okMar {
		t.Fatalf("starts %+v", byStart)
	}
	for _, b := range []Bill{feb, mar} {
		if b.PaidByParticipantID != "" || b.InvoiceRef != "" || b.Notes != "meter" || b.AmountCents != 10000 || b.CreatedBy != ana {
			t.Fatalf("copy %+v", b)
		}
		if len(b.Shares) != 2 {
			t.Fatalf("shares %+v", b.Shares)
		}
		for _, sh := range b.Shares {
			if sh.Paid {
				t.Fatalf("paid share %+v", sh)
			}
		}
	}
	if feb.PeriodEnd.Format("2006-01-02") != "2026-02-28" || feb.DueDate.Format("2006-01-02") != "2026-03-10" {
		t.Fatalf("feb dates %s %s", feb.PeriodEnd.Format("2006-01-02"), feb.DueDate.Format("2006-01-02"))
	}
	if mar.PeriodEnd.Format("2006-01-02") != "2026-03-31" || mar.DueDate.Format("2006-01-02") != "2026-04-10" {
		t.Fatalf("mar dates %s %s", mar.PeriodEnd.Format("2006-01-02"), mar.DueDate.Format("2006-01-02"))
	}
	if err := st.EnsureRecurring(ctx, home, today); err != nil {
		t.Fatal(err)
	}
	again, err := st.Bills(ctx, home)
	if err != nil || len(again) != 3 {
		t.Fatal(err, len(again))
	}

	if err := st.SetRepeats(ctx, home, util, false); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureRecurring(ctx, home, date(2026, 5, 1)); err != nil {
		t.Fatal(err)
	}
	stopped, err := st.Bills(ctx, home)
	if err != nil || len(stopped) != 3 {
		t.Fatal(err, len(stopped))
	}
}

func TestEnsureRecurringSkipsInactivePeople(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	ana, home, anaPart := testHousehold(t, st)
	kid := testParticipant(t, st, home, "Kid")
	util := testUtility(t, st, home)
	if err := st.SetRepeats(ctx, home, util, true); err != nil {
		t.Fatal(err)
	}
	_, err := st.CreateBill(ctx, NewBill{
		HouseholdID: home,
		UtilityID:   util,
		PeriodStart: date(2026, 1, 1),
		PeriodEnd:   date(2026, 1, 31),
		DueDate:     date(2026, 2, 10),
		AmountCents: 10000,
		SplitMethod: "equal",
		CreatedBy:   ana,
		Shares: []NewShare{
			{ParticipantID: anaPart, AmountCents: 4000},
			{ParticipantID: kid, AmountCents: 6000},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE participants SET active = 0 WHERE id = ?`, kid); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureRecurring(ctx, home, date(2026, 3, 1)); err != nil {
		t.Fatal(err)
	}
	bills, err := st.Bills(ctx, home)
	if err != nil || len(bills) != 1 {
		t.Fatal(err, len(bills))
	}
}

func TestAddBillMonthSnapsMonthEnd(t *testing.T) {
	start, end, due := addBillMonth(date(2026, 1, 1), date(2026, 1, 31), date(2026, 2, 10))
	if start.Format("2006-01-02") != "2026-02-01" || end.Format("2006-01-02") != "2026-02-28" || due.Format("2006-01-02") != "2026-03-10" {
		t.Fatalf("%s %s %s", start.Format("2006-01-02"), end.Format("2006-01-02"), due.Format("2006-01-02"))
	}
	_, leap, _ := addBillMonth(date(2024, 1, 1), date(2024, 1, 31), date(2024, 2, 10))
	if leap.Format("2006-01-02") != "2024-02-29" {
		t.Fatal(leap.Format("2006-01-02"))
	}
	_, mid, _ := addBillMonth(date(2026, 1, 1), date(2026, 1, 15), date(2026, 1, 20))
	if mid.Format("2006-01-02") != "2026-02-15" {
		t.Fatal(mid.Format("2006-01-02"))
	}
}

func testHousehold(t *testing.T, st *Store) (userID, householdID, participantID string) {
	t.Helper()
	ctx := context.Background()
	user, err := st.CreateUser(ctx, "Ana", "ana@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	home, err := st.CreateHousehold(ctx, "Home", "USD", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	members, err := st.Members(ctx, home.ID)
	if err != nil || len(members) != 1 {
		t.Fatal(err, len(members))
	}
	return user.ID, home.ID, members[0].ParticipantID
}

func testParticipant(t *testing.T, st *Store, householdID, name string) string {
	t.Helper()
	id, err := st.AddParticipant(context.Background(), householdID, name, "")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func testUtility(t *testing.T, st *Store, householdID string) string {
	t.Helper()
	util, err := st.CreateUtility(context.Background(), householdID, "Electricity", "City Power", "electric")
	if err != nil {
		t.Fatal(err)
	}
	return util.ID
}

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
