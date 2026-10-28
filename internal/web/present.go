package web

import (
	"fmt"
	"sort"
	"time"

	"hearth/internal/billing"
	"hearth/internal/money"
	"hearth/internal/store"
)

func categories() []Choice {
	return []Choice{
		{Value: "electric", Label: "Electricity"},
		{Value: "water", Label: "Water"},
		{Value: "gas", Label: "Gas"},
		{Value: "internet", Label: "Internet"},
		{Value: "trash", Label: "Trash"},
		{Value: "rent", Label: "Rent"},
		{Value: "other", Label: "Other"},
	}
}

func categoryLabel(value string) string {
	for _, c := range categories() {
		if c.Value == value {
			return c.Label
		}
	}
	return value
}

func validCategory(value string) bool {
	for _, c := range categories() {
		if c.Value == value {
			return true
		}
	}
	return false
}

func noticeText(code string) string {
	switch code {
	case "household":
		return "Household created."
	case "joined":
		return "You've joined the household."
	case "bill":
		return "Bill recorded."
	case "payment":
		return "Payment recorded."
	case "deleted":
		return "Bill deleted."
	case "invite":
		return "Invite created. Send the link to that person. It works once."
	case "revoked":
		return "Invite revoked."
	case "removed":
		return "Member removed."
	case "person":
		return "Added. They can be included on a bill without an account."
	case "utility":
		return "Utility added."
	case "exemptions":
		return "Default exemptions saved."
	case "need-utility":
		return "Add a utility before recording a bill."
	case "renamed":
		return "Household name updated."
	case "provider":
		return "Recorded who paid the provider."
	default:
		return ""
	}
}

func toPeople(members []store.Member, self string) []Person {
	out := make([]Person, 0, len(members))
	for _, m := range members {
		out = append(out, Person{
			ID:         m.ParticipantID,
			Name:       m.Name,
			Email:      m.Email,
			Role:       m.Role,
			Joined:     m.Joined.In(time.Local).Format("Jan 2, 2006"),
			IsSelf:     m.UserID != "" && m.UserID == self,
			IsOwner:    m.Role == "owner",
			HasAccount: m.UserID != "",
		})
	}
	return out
}

func utilityOptions(list []store.Utility) []UtilityOption {
	out := make([]UtilityOption, 0, len(list))
	for _, u := range list {
		label := u.Name
		if u.Provider != "" {
			label += " · " + u.Provider
		}
		out = append(out, UtilityOption{ID: u.ID, Label: label})
	}
	return out
}

func presentBillRows(bills []store.Bill, selfID, currency string) []BillRow {
	rows := make([]BillRow, 0, len(bills))
	for _, b := range bills {
		rows = append(rows, presentBillRow(b, selfID, currency))
	}
	return rows
}

func presentBillRow(b store.Bill, selfID, currency string) BillRow {
	status := b.Status()
	payer := "Not paid yet"
	if b.PaidByName != "" {
		payer = b.PaidByName
	}
	return BillRow{
		ID:          b.ID,
		Utility:     b.UtilityName,
		Category:    categoryLabel(b.Category),
		Period:      formatPeriod(b.PeriodStart, b.PeriodEnd),
		Due:         b.DueDate.Format("Jan 2, 2006"),
		DueClass:    dueClass(b.DueDate, status),
		Amount:      money.Format(b.AmountCents, currency),
		Status:      status,
		StatusLabel: statusLabel(status),
		Payer:       payer,
		Yours:       yourShare(b, selfID, currency),
	}
}

func presentBill(b store.Bill, selfID, currency string, canDelete bool) BillDetail {
	status := b.Status()
	awaiting := b.PaidByParticipantID == ""
	payer := "Not paid yet"
	if b.PaidByName != "" {
		payer = b.PaidByName
	}
	detail := BillDetail{
		ID:            b.ID,
		Utility:       b.UtilityName,
		Provider:      b.Provider,
		Category:      categoryLabel(b.Category),
		Period:        formatPeriod(b.PeriodStart, b.PeriodEnd),
		Due:           b.DueDate.Format("Jan 2, 2006"),
		DueClass:      dueClass(b.DueDate, status),
		Amount:        money.Format(b.AmountCents, currency),
		Status:        status,
		StatusLabel:   statusLabel(status),
		Payer:         payer,
		AwaitingPayer: awaiting,
		InvoiceRef:    b.InvoiceRef,
		Notes:         b.Notes,
		SplitLabel:    splitLabel(b.SplitMethod),
		Recorded:      b.CreatedAt.In(time.Local).Format("Jan 2, 2006"),
		CanDelete:     canDelete,
		ShowWeights:   b.SplitMethod == "weights",
	}
	for _, sh := range b.Shares {
		left := sh.AmountCents - sh.PaidCents
		covered := !awaiting && sh.ParticipantID == b.PaidByParticipantID
		payable := !awaiting && !covered && left > 0
		leftLabel := money.Format(left, currency)
		paidLabel := money.Format(sh.PaidCents, currency)
		switch {
		case covered:
			leftLabel = "Covered"
			paidLabel = "—"
		case !awaiting && left <= 0:
			leftLabel = "Settled"
		}
		detail.Shares = append(detail.Shares, ShareRow{
			UserID:    sh.ParticipantID,
			Name:      sh.Name,
			Amount:    money.Format(sh.AmountCents, currency),
			Paid:      paidLabel,
			Left:      leftLabel,
			LeftInput: money.Input(max64(left, 0)),
			Weight:    sh.Weight,
			Covered:   covered,
			Payable:   payable,
		})
	}
	return detail
}

func buildBalances(selfID, currency string, bills []store.Bill) ([]BalanceLine, int64, int64) {
	names := map[string]string{}
	var debts []billing.Debt
	for _, b := range bills {
		if b.PaidByParticipantID != "" && b.PaidByName != "" {
			names[b.PaidByParticipantID] = b.PaidByName
		}
		for _, sh := range b.Shares {
			names[sh.ParticipantID] = sh.Name
		}
		debts = append(debts, b.Debts()...)
	}
	net := billing.Net(debts)
	sort.Slice(net, func(i, j int) bool {
		if net[i].Cents != net[j].Cents {
			return net[i].Cents > net[j].Cents
		}
		if names[net[i].FromID] != names[net[j].FromID] {
			return names[net[i].FromID] < names[net[j].FromID]
		}
		return names[net[i].ToID] < names[net[j].ToID]
	})
	var youOwe, owedToYou int64
	lines := make([]BalanceLine, 0, len(net))
	for _, d := range net {
		if d.FromID == selfID {
			youOwe += d.Cents
		}
		if d.ToID == selfID {
			owedToYou += d.Cents
		}
		from := names[d.FromID]
		to := names[d.ToID]
		if from == "" {
			from = "Someone"
		}
		if to == "" {
			to = "Someone"
		}
		lines = append(lines, BalanceLine{
			Text:   from + " owes " + to,
			Amount: money.Format(d.Cents, currency),
			Yours:  d.FromID == selfID || d.ToID == selfID,
		})
	}
	return lines, youOwe, owedToYou
}

func yourShare(b store.Bill, selfID, currency string) string {
	var share *store.Share
	for i := range b.Shares {
		if b.Shares[i].ParticipantID == selfID {
			share = &b.Shares[i]
			break
		}
	}
	if share == nil {
		return ""
	}
	if b.PaidByParticipantID == "" {
		return "Your share " + money.Format(share.AmountCents, currency)
	}
	if b.PaidByParticipantID == selfID {
		return "You paid"
	}
	left := share.AmountCents - share.PaidCents
	if left <= 0 {
		return "Settled"
	}
	return "You owe " + money.Format(left, currency)
}

func formatPeriod(start, end time.Time) string {
	if start.Year() == end.Year() && start.Month() == end.Month() {
		return fmt.Sprintf("%s %d–%d, %d", start.Month().String()[:3], start.Day(), end.Day(), start.Year())
	}
	if start.Year() == end.Year() {
		return fmt.Sprintf("%s %d – %s %d, %d", start.Month().String()[:3], start.Day(), end.Month().String()[:3], end.Day(), start.Year())
	}
	return start.Format("Jan 2, 2006") + " – " + end.Format("Jan 2, 2006")
}

func dueClass(due time.Time, status string) string {
	if status == billing.StatusSettled {
		return ""
	}
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	if due.Before(today) {
		return "overdue"
	}
	return ""
}

func statusLabel(status string) string {
	switch status {
	case billing.StatusUnpaid:
		return "Not paid"
	case billing.StatusOpen:
		return "Settling"
	case billing.StatusSettled:
		return "Settled"
	default:
		return status
	}
}

func splitLabel(method string) string {
	switch method {
	case "equal":
		return "Split equally"
	case "weights":
		return "Split by weight"
	case "amounts":
		return "Custom amounts"
	default:
		return method
	}
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
