package web

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
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
	case "paid":
		return "Share marked as paid."
	case "unpaid":
		return "Share marked as not paid."
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
	case "utility-edited":
		return "Utility updated."
	case "exemptions":
		return "Default exemptions saved."
	case "repeat":
		return "Monthly repeat updated."
	case "edited":
		return "Bill updated."
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
		covered := !awaiting && sh.ParticipantID == b.PaidByParticipantID
		paidLabel := "Not paid"
		leftLabel := "Not paid"
		switch {
		case covered:
			paidLabel = "—"
			leftLabel = "Covered"
		case sh.Paid:
			paidLabel = "Paid"
			leftLabel = "Settled"
		}
		detail.Shares = append(detail.Shares, ShareRow{
			UserID:   sh.ParticipantID,
			Name:     sh.Name,
			Amount:   money.Format(sh.AmountCents, currency),
			Paid:     paidLabel,
			Left:     leftLabel,
			Weight:   sh.Weight,
			Covered:  covered,
			Payable:  !covered && !sh.Paid,
			CanClear: !covered && sh.Paid,
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
	itemized := buildItemizedDebts(currency, bills)
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
			Text:     from + " owes " + to,
			Amount:   money.Format(d.Cents, currency),
			Yours:    d.FromID == selfID || d.ToID == selfID,
			Itemized: itemized[debtPair{fromID: d.FromID, toID: d.ToID}],
		})
	}
	return lines, youOwe, owedToYou
}

type itemizedDebtEntry struct {
	fromID      string
	toID        string
	providerKey string
	line        ItemizedDebtLine
	cents       int64
}

type debtPair struct {
	fromID string
	toID   string
}

func buildItemizedDebts(currency string, bills []store.Bill) map[debtPair][]ItemizedDebtLine {
	var items []itemizedDebtEntry
	for _, b := range bills {
		provider := strings.TrimSpace(b.Provider)
		if provider == "" {
			provider = b.UtilityName
		}
		for _, debt := range b.Debts() {
			items = append(items, itemizedDebtEntry{
				fromID:      debt.FromID,
				toID:        debt.ToID,
				providerKey: strings.ToLower(provider),
				cents:       debt.Cents,
				line: ItemizedDebtLine{
					Text:   provider,
					Amount: money.Format(debt.Cents, currency),
				},
			})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		aLow, aHigh := participantPair(items[i])
		bLow, bHigh := participantPair(items[j])
		if aLow != bLow {
			return aLow < bLow
		}
		if aHigh != bHigh {
			return aHigh < bHigh
		}
		if items[i].fromID != items[j].fromID {
			return items[i].fromID < items[j].fromID
		}
		if items[i].providerKey != items[j].providerKey {
			return items[i].providerKey < items[j].providerKey
		}
		return items[i].line.Text < items[j].line.Text
	})
	// Cancel reverse debts between each pair so the itemized rows match the net totals.
	for start := 0; start < len(items); {
		end := start + 1
		for end < len(items) && sameParticipantPair(items[start], items[end]) {
			end++
		}
		forward, reverse := make([]int, 0), make([]int, 0)
		var forwardTotal, reverseTotal int64
		for i := start; i < end; i++ {
			if items[i].fromID < items[i].toID {
				forward = append(forward, i)
				forwardTotal += items[i].cents
			} else {
				reverse = append(reverse, i)
				reverseTotal += items[i].cents
			}
		}
		cancel := forwardTotal
		if reverseTotal < cancel {
			cancel = reverseTotal
		}
		reduceDebtItems(items, forward, cancel)
		reduceDebtItems(items, reverse, cancel)
		start = end
	}
	type providerDebt struct {
		line  ItemizedDebtLine
		cents int64
	}
	byProvider := make(map[debtPair]map[string]*providerDebt)
	for _, item := range items {
		if item.cents == 0 {
			continue
		}
		key := debtPair{fromID: item.fromID, toID: item.toID}
		if byProvider[key] == nil {
			byProvider[key] = make(map[string]*providerDebt)
		}
		group := byProvider[key][item.providerKey]
		if group == nil {
			group = &providerDebt{line: item.line}
			byProvider[key][item.providerKey] = group
		}
		group.cents += item.cents
	}
	out := make(map[debtPair][]ItemizedDebtLine, len(byProvider))
	for key, providers := range byProvider {
		for _, group := range providers {
			group.line.Amount = money.Format(group.cents, currency)
			out[key] = append(out[key], group.line)
		}
		sort.Slice(out[key], func(i, j int) bool { return out[key][i].Text < out[key][j].Text })
	}
	return out
}

func sameParticipantPair(a, b itemizedDebtEntry) bool {
	aLow, aHigh := participantPair(a)
	bLow, bHigh := participantPair(b)
	return aLow == bLow && aHigh == bHigh
}

func participantPair(debt itemizedDebtEntry) (string, string) {
	if debt.fromID < debt.toID {
		return debt.fromID, debt.toID
	}
	return debt.toID, debt.fromID
}

func reduceDebtItems(items []itemizedDebtEntry, indices []int, amount int64) {
	for _, i := range indices {
		reduction := items[i].cents
		if reduction > amount {
			reduction = amount
		}
		items[i].cents -= reduction
		amount -= reduction
		if amount == 0 {
			return
		}
	}
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
	if share.Paid {
		return "Settled"
	}
	return "You owe " + money.Format(share.AmountCents, currency)
}

func contributions(bills []store.Bill) map[string]int64 {
	out := map[string]int64{}
	for _, b := range bills {
		if b.PaidByParticipantID == "" {
			continue
		}
		var othersPaid int64
		for _, sh := range b.Shares {
			if sh.ParticipantID == b.PaidByParticipantID || !sh.Paid {
				continue
			}
			othersPaid += sh.AmountCents
			out[sh.ParticipantID] += sh.AmountCents
		}
		out[b.PaidByParticipantID] += b.AmountCents - othersPaid
	}
	return out
}

func contributionLabel(cents, total int64, currency string) string {
	amount := money.Format(cents, currency)
	if total <= 0 || cents <= 0 {
		return amount + " · 0%"
	}
	pct := (cents*100 + total/2) / total
	return fmt.Sprintf("%s · %d%%", amount, pct)
}

func normalizeChart(mode string) string {
	switch mode {
	case "household", "people":
		return mode
	default:
		return "both"
	}
}

var personColors = []string{"#241c16", "#6d5e51", "#9a451c", "#1d6b45", "#8d2e2e", "#3f5d4a", "#7a5b3a"}

func spendingChart(bills []store.Bill, mode, currency string) string {
	type bucket struct {
		total  int64
		people map[string]int64
	}
	if len(bills) == 0 {
		return ""
	}
	names := map[string]string{}
	var start, end time.Time
	byMonth := map[string]*bucket{}
	for i, b := range bills {
		month := time.Date(b.PeriodStart.Year(), b.PeriodStart.Month(), 1, 0, 0, 0, 0, time.Local)
		if i == 0 || month.Before(start) {
			start = month
		}
		if i == 0 || month.After(end) {
			end = month
		}
		key := month.Format("2006-01")
		slot := byMonth[key]
		if slot == nil {
			slot = &bucket{people: map[string]int64{}}
			byMonth[key] = slot
		}
		slot.total += b.AmountCents
		for _, sh := range b.Shares {
			names[sh.ParticipantID] = sh.Name
			slot.people[sh.ParticipantID] += sh.AmountCents
		}
	}
	people := make([]chartPerson, 0, len(names))
	for id, name := range names {
		people = append(people, chartPerson{id: id, name: name})
	}
	sort.Slice(people, func(i, j int) bool { return people[i].name < people[j].name })
	for i := range people {
		people[i].color = personColors[i%len(personColors)]
	}
	var months []time.Time
	for m := start; !m.After(end); m = m.AddDate(0, 1, 0) {
		months = append(months, m)
	}
	labels := make([]string, len(months))
	for i, m := range months {
		labels[i] = m.Format("Jan 2006")
	}
	payload := chartPayload{Symbol: currencySymbol(currency), Labels: labels}
	if mode != "people" {
		cents := make([]int64, len(months))
		for i, m := range months {
			if slot := byMonth[m.Format("2006-01")]; slot != nil {
				cents[i] = slot.total
			}
		}
		payload.Datasets = append(payload.Datasets, chartDataset{Label: "Household", Color: "#c4622d", Cents: cents, Order: 1})
	}
	if mode != "household" {
		for _, p := range people {
			cents := make([]int64, len(months))
			for i, m := range months {
				if slot := byMonth[m.Format("2006-01")]; slot != nil {
					cents[i] = slot.people[p.id]
				}
			}
			payload.Datasets = append(payload.Datasets, chartDataset{Label: p.name, Color: p.color, Cents: cents, Order: 0})
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return string(raw)
}

type chartPerson struct {
	id, name, color string
}

type chartDataset struct {
	Label string  `json:"label"`
	Color string  `json:"color"`
	Cents []int64 `json:"cents"`
	Order int     `json:"order"`
}

type chartPayload struct {
	Symbol   string         `json:"symbol"`
	Labels   []string       `json:"labels"`
	Datasets []chartDataset `json:"datasets"`
}

func currencySymbol(code string) string {
	return strings.TrimSuffix(money.Format(0, code), "0.00")
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
