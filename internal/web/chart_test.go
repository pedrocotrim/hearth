package web

import (
	"encoding/json"
	"testing"
	"time"

	"hearth/internal/store"
)

func TestSpendingChartJSON(t *testing.T) {
	bills := []store.Bill{{
		PeriodStart: time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local),
		AmountCents: 10000,
		Shares: []store.Share{
			{ParticipantID: "a", Name: "Ana", AmountCents: 6000},
			{ParticipantID: "b", Name: "Luis", AmountCents: 4000},
		},
	}, {
		PeriodStart: time.Date(2026, 3, 1, 0, 0, 0, 0, time.Local),
		AmountCents: 5000,
		Shares: []store.Share{
			{ParticipantID: "a", Name: "Ana", AmountCents: 5000},
		},
	}}
	var payload chartPayload
	if err := json.Unmarshal([]byte(spendingChart(bills, "both", "USD")), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Symbol != "$" {
		t.Fatalf("symbol %q", payload.Symbol)
	}
	if got, want := len(payload.Labels), 3; got != want {
		t.Fatalf("labels %#v", payload.Labels)
	}
	if payload.Labels[1] != "Feb 2026" {
		t.Fatalf("empty month missing: %#v", payload.Labels)
	}
	byName := map[string][]int64{}
	for _, series := range payload.Datasets {
		byName[series.Label] = series.Cents
	}
	if got := byName["Household"]; len(got) != 3 || got[0] != 10000 || got[1] != 0 || got[2] != 5000 {
		t.Fatalf("household %#v", got)
	}
	if got := byName["Ana"]; len(got) != 3 || got[0] != 6000 || got[2] != 5000 {
		t.Fatalf("ana %#v", got)
	}
	if got := byName["Luis"]; len(got) != 3 || got[0] != 4000 || got[2] != 0 {
		t.Fatalf("luis %#v", got)
	}
}
