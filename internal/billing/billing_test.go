package billing

import (
	"errors"
	"testing"
)

func TestSplitEqualRemainder(t *testing.T) {
	got, err := SplitEqual(100, []string{"c", "a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if sum(got) != 100 {
		t.Fatalf("sum = %d", sum(got))
	}
	amounts := map[string]int64{}
	for _, p := range got {
		amounts[p.UserID] = p.AmountCents
	}
	if amounts["a"] != 34 || amounts["b"] != 33 || amounts["c"] != 33 {
		t.Fatalf("amounts = %#v", amounts)
	}
}

func TestSplitWeighted(t *testing.T) {
	got, err := SplitWeighted(100, []Portion{
		{UserID: "a", Weight: 1},
		{UserID: "b", Weight: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	if sum(got) != 100 {
		t.Fatalf("sum = %d", sum(got))
	}
	amounts := map[string]int64{}
	for _, p := range got {
		amounts[p.UserID] = p.AmountCents
	}
	if amounts["a"] != 25 || amounts["b"] != 75 {
		t.Fatalf("amounts = %#v", amounts)
	}
}

func TestSplitFixedMismatch(t *testing.T) {
	_, err := SplitFixed(100, []Portion{
		{UserID: "a", AmountCents: 40},
		{UserID: "b", AmountCents: 50},
	})
	var mismatch SumMismatchError
	if !errors.As(err, &mismatch) || mismatch.Got != 90 || mismatch.Want != 100 {
		t.Fatalf("err = %v", err)
	}
}

func TestNetPairwise(t *testing.T) {
	got := Net([]Debt{
		{FromID: "a", ToID: "b", Cents: 100},
		{FromID: "b", ToID: "a", Cents: 40},
		{FromID: "a", ToID: "b", Cents: 25},
	})
	if len(got) != 1 || got[0].FromID != "a" || got[0].ToID != "b" || got[0].Cents != 85 {
		t.Fatalf("net = %#v", got)
	}
}

func TestNetCycleClears(t *testing.T) {
	got := Net([]Debt{
		{FromID: "a", ToID: "b", Cents: 30},
		{FromID: "b", ToID: "a", Cents: 30},
	})
	if len(got) != 0 {
		t.Fatalf("net = %#v", got)
	}
}

func TestDeriveStatus(t *testing.T) {
	shares := []ShareState{
		{UserID: "payer", AmountCents: 50},
		{UserID: "other", AmountCents: 50},
	}
	if DeriveStatus("", shares) != StatusUnpaid {
		t.Fatal("unpaid")
	}
	if DeriveStatus("payer", shares) != StatusOpen {
		t.Fatal("open")
	}
	shares[1].PaidCents = 50
	if DeriveStatus("payer", shares) != StatusSettled {
		t.Fatal("settled")
	}
	if debts := Debts("payer", []ShareState{
		{UserID: "payer", AmountCents: 50},
		{UserID: "other", AmountCents: 50, PaidCents: 20},
	}); len(debts) != 1 || debts[0].Cents != 30 || debts[0].FromID != "other" {
		t.Fatalf("debts = %#v", debts)
	}
}

func sum(parts []Portion) int64 {
	var n int64
	for _, p := range parts {
		n += p.AmountCents
	}
	return n
}
