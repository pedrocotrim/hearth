package web

import (
	"fmt"
	"html"
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

type ChartSwatch struct {
	Name  string
	Color string
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

func spendingChart(bills []store.Bill, mode string) (string, []ChartSwatch) {
	type bucket struct {
		total  int64
		people map[string]int64
	}
	if len(bills) == 0 {
		return "", nil
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
	swatches := make([]ChartSwatch, 0, len(people)+1)
	if mode != "people" {
		swatches = append(swatches, ChartSwatch{Name: "Household", Color: "#c4622d"})
	}
	for i := range people {
		people[i].color = personColors[i%len(personColors)]
		if mode != "household" {
			swatches = append(swatches, ChartSwatch{Name: people[i].name, Color: people[i].color})
		}
	}
	var months []time.Time
	for m := start; !m.After(end); m = m.AddDate(0, 1, 0) {
		months = append(months, m)
	}
	var max int64
	for _, m := range months {
		slot := byMonth[m.Format("2006-01")]
		if slot == nil {
			continue
		}
		if slot.total > max {
			max = slot.total
		}
	}
	if max <= 0 {
		max = 1
	}
	const (
		width  = 720
		height = 240
		left   = 8
		right  = 8
		top    = 12
		bottom = 32
	)
	plotW := float64(width - left - right)
	plotH := float64(height - top - bottom)
	baseY := float64(top) + plotH
	slotW := plotW / float64(len(months))
	var svg strings.Builder
	fmt.Fprintf(&svg, `<svg class="chart" viewBox="0 0 %d %d" role="img" aria-label="Spending by month">`, width, height)
	fmt.Fprintf(&svg, `<line x1="%d" y1="%.1f" x2="%d" y2="%.1f" stroke="#ddcfc0"/>`, left, baseY, width-right, baseY)
	for i, m := range months {
		key := m.Format("2006-01")
		slot := byMonth[key]
		if slot == nil {
			slot = &bucket{people: map[string]int64{}}
		}
		x0 := float64(left) + float64(i)*slotW
		switch mode {
		case "household":
			drawBar(&svg, x0+slotW*0.18, slotW*0.64, baseY, plotH, slot.total, max, "#c4622d", 1)
		case "people":
			drawPeople(&svg, x0, slotW, baseY, plotH, max, slot.people, people)
		default:
			drawBar(&svg, x0+slotW*0.12, slotW*0.76, baseY, plotH, slot.total, max, "#c4622d", 0.35)
			drawPeople(&svg, x0+slotW*0.18, slotW*0.64, baseY, plotH, max, slot.people, people)
		}
		label := m.Format("Jan")
		if m.Month() == time.January || i == 0 {
			label = m.Format("Jan 06")
		}
		fmt.Fprintf(&svg, `<text x="%.1f" y="%d" text-anchor="middle" fill="#6d5e51" font-size="11">%s</text>`, x0+slotW/2, height-10, html.EscapeString(label))
	}
	svg.WriteString(`</svg>`)
	return svg.String(), swatches
}

type chartPerson struct {
	id, name, color string
}

func drawPeople(svg *strings.Builder, x0, slotW, baseY, plotH float64, max int64, values map[string]int64, people []chartPerson) {
	if len(people) == 0 {
		return
	}
	gap := slotW * 0.08 / float64(len(people))
	barW := (slotW*0.84)/float64(len(people)) - gap
	if barW < 2 {
		barW = 2
	}
	for i, p := range people {
		drawBar(svg, x0+slotW*0.08+float64(i)*(barW+gap), barW, baseY, plotH, values[p.id], max, p.color, 1)
	}
}

func drawBar(svg *strings.Builder, x, w, baseY, plotH float64, value, max int64, color string, opacity float64) {
	if value <= 0 || w <= 0 {
		return
	}
	h := plotH * float64(value) / float64(max)
	if h < 1 {
		h = 1
	}
	fmt.Fprintf(svg, `<rect x="%.2f" y="%.2f" width="%.2f" height="%.2f" rx="3" fill="%s" fill-opacity="%.2f"/>`, x, baseY-h, w, h, color, opacity)
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
