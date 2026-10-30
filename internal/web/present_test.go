package web

import (
	"testing"
	"time"

	"hearth/internal/store"
)

func TestBuildItemizedDebtsKeepsOutstandingAmountsPerBill(t *testing.T) {
	bills := []store.Bill{
		{
			UtilityName:         "Power",
			PeriodStart:         time.Date(2026, time.January, 1, 0, 0, 0, 0, time.Local),
			PeriodEnd:           time.Date(2026, time.January, 31, 0, 0, 0, 0, time.Local),
			PaidByParticipantID: "ana",
			PaidByName:          "Ana",
			Shares: []store.Share{
				{ParticipantID: "ana", Name: "Ana", AmountCents: 5000},
				{ParticipantID: "luis", Name: "Luis", AmountCents: 3000},
				{ParticipantID: "kid", Name: "Kid", AmountCents: 2000, Paid: true},
			},
		},
		{
			UtilityName:         "Internet",
			PeriodStart:         time.Date(2026, time.February, 1, 0, 0, 0, 0, time.Local),
			PeriodEnd:           time.Date(2026, time.February, 28, 0, 0, 0, 0, time.Local),
			PaidByParticipantID: "luis",
			PaidByName:          "Luis",
			Shares: []store.Share{
				{ParticipantID: "luis", Name: "Luis", AmountCents: 6000},
				{ParticipantID: "ana", Name: "Ana", AmountCents: 4000},
			},
		},
		{
			UtilityName:         "Water",
			PeriodStart:         time.Date(2026, time.March, 1, 0, 0, 0, 0, time.Local),
			PeriodEnd:           time.Date(2026, time.March, 31, 0, 0, 0, 0, time.Local),
			PaidByParticipantID: "ana",
			PaidByName:          "Ana",
			Shares: []store.Share{
				{ParticipantID: "ana", Name: "Ana", AmountCents: 1000},
				{ParticipantID: "mara", Name: "Mara", AmountCents: 1000},
			},
		},
	}

	balances, totalOwed, totalOwedToYou := buildBalances("ana", "USD", bills)
	if totalOwed != 1000 || totalOwedToYou != 1000 || len(balances) != 2 || balances[0].Amount != "$10.00" || balances[1].Amount != "$10.00" {
		t.Fatalf("netted balances %d owed, %d owed to, %+v", totalOwed, totalOwedToYou, balances)
	}
	foundAnaOwesLuis, foundMaraOwesAna := false, false
	for _, line := range balances {
		switch line.Text {
		case "Ana owes Luis":
			foundAnaOwesLuis = true
			if len(line.Itemized) != 1 || line.Itemized[0].Text != "Internet" || line.Itemized[0].Amount != "$10.00" {
				t.Errorf("Ana/Luis itemization %+v", line.Itemized)
			}
		case "Mara owes Ana":
			foundMaraOwesAna = true
			if len(line.Itemized) != 1 || line.Itemized[0].Text != "Water" || line.Itemized[0].Amount != "$10.00" {
				t.Errorf("Mara/Ana itemization %+v", line.Itemized)
			}
		}
	}
	if !foundAnaOwesLuis || !foundMaraOwesAna {
		t.Fatalf("missing balance rows %+v", balances)
	}
}

func TestBuildBalancesSumsBillsFromSameProvider(t *testing.T) {
	bills := []store.Bill{
		{
			UtilityName:         "Electricity",
			Provider:            "City Power",
			PeriodStart:         time.Date(2026, time.January, 1, 0, 0, 0, 0, time.Local),
			PeriodEnd:           time.Date(2026, time.January, 31, 0, 0, 0, 0, time.Local),
			PaidByParticipantID: "ana",
			PaidByName:          "Ana",
			Shares: []store.Share{
				{ParticipantID: "ana", Name: "Ana", AmountCents: 5000},
				{ParticipantID: "luis", Name: "Luis", AmountCents: 4000},
			},
		},
		{
			UtilityName:         "Gas",
			Provider:            " city power ",
			PeriodStart:         time.Date(2026, time.February, 1, 0, 0, 0, 0, time.Local),
			PeriodEnd:           time.Date(2026, time.February, 28, 0, 0, 0, 0, time.Local),
			PaidByParticipantID: "ana",
			PaidByName:          "Ana",
			Shares: []store.Share{
				{ParticipantID: "ana", Name: "Ana", AmountCents: 5000},
				{ParticipantID: "luis", Name: "Luis", AmountCents: 6000},
			},
		},
	}

	balances, totalOwed, _ := buildBalances("luis", "USD", bills)
	if totalOwed != 10000 || len(balances) != 1 {
		t.Fatalf("balances total %d rows %+v", totalOwed, balances)
	}
	if len(balances[0].Itemized) != 1 || balances[0].Itemized[0].Text != "City Power" || balances[0].Itemized[0].Amount != "$100.00" {
		t.Fatalf("same-provider itemization %+v", balances[0].Itemized)
	}
}
