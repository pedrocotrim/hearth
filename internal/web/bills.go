package web

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"hearth/internal/billing"
	"hearth/internal/money"
	"hearth/internal/store"
	"hearth/internal/token"
)

func (s *Server) bills(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.mustScope(w, r)
	if !ok {
		return
	}
	bills, err := s.store.Bills(r.Context(), sc.Household.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	filter := normalizeFilter(r.URL.Query().Get("status"))
	if filter != "all" {
		filtered := make([]store.Bill, 0, len(bills))
		for _, bill := range bills {
			if bill.Status() == filter {
				filtered = append(filtered, bill)
			}
		}
		bills = filtered
	}
	v := s.shell(r, sc, "Bills", "bills")
	v.Content = "bills"
	v.Filter = filter
	v.BillRows = presentBillRows(bills, sc.SelfID, sc.Household.Currency)
	s.render(w, http.StatusOK, "app", v)
}

func (s *Server) newBill(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.mustScope(w, r)
	if !ok {
		return
	}
	s.renderBillForm(w, r, sc, defaultBillForm(), nil, "", http.StatusOK)
}

func (s *Server) splitFields(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.mustScope(w, r)
	if !ok {
		return
	}
	members, err := s.store.Members(r.Context(), sc.Household.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	people := toPeople(members, sc.User.ID)
	form := readBillForm(r, people)
	if err := s.resolveIncludes(r, sc, &form, people, true); err != nil {
		s.fail(w, r, err)
		return
	}
	people = applyIncludeFlags(people, form)
	s.applySplitPreview(&form, people, sc.Household.Currency)
	s.renderPartial(w, "split-fields", View{
		People:   people,
		Form:     form,
		Currency: sc.Household.Currency,
		Fields:   map[string]string{},
	})
}

func (s *Server) postBill(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.mustScope(w, r)
	if !ok {
		return
	}
	members, err := s.store.Members(r.Context(), sc.Household.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	utils, err := s.store.Utilities(r.Context(), sc.Household.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	people := toPeople(members, sc.User.ID)
	form := readBillForm(r, people)
	if err := s.resolveIncludes(r, sc, &form, people, false); err != nil {
		s.fail(w, r, err)
		return
	}
	people = applyIncludeFlags(people, form)
	fields := map[string]string{}

	utilityOK := false
	for _, u := range utils {
		if u.ID == form.UtilityID {
			utilityOK = true
			break
		}
	}
	if !utilityOK {
		fields["utility_id"] = "Choose a utility."
	}
	start, startErr := parseDateField(form.PeriodStart)
	end, endErr := parseDateField(form.PeriodEnd)
	due, dueErr := parseDateField(form.DueDate)
	if startErr != nil {
		fields["period_start"] = "Enter a valid date."
	}
	if endErr != nil {
		fields["period_end"] = "Enter a valid date."
	} else if startErr == nil && end.Before(start) {
		fields["period_end"] = "The period can't end before it starts."
	}
	if dueErr != nil {
		fields["due_date"] = "Enter a valid date."
	}
	invoice, invoiceOK := cleanOptional(form.InvoiceRef, 80)
	notes, notesOK := cleanNotes(form.Notes, 500)
	if !invoiceOK {
		fields["invoice_ref"] = "Keep the reference under 80 characters."
	}
	if !notesOK {
		fields["notes"] = "Keep the note under 500 characters."
	}
	if form.PaidBy != "" {
		known := false
		for _, p := range includedPeople(form, people) {
			if p.ID == form.PaidBy {
				known = true
				break
			}
		}
		if !known {
			fields["paid_by"] = "Choose someone included in this bill."
		}
	}

	var portions []billing.Portion
	total, amountErr := money.Parse(form.Amount)
	if amountErr != nil || total <= 0 {
		fields["amount"] = "Enter an amount greater than zero."
	} else if len(fields) == 0 {
		portions, err = portionsFromForm(form, people, total)
		if err != nil {
			fields["split"] = userSplitError(err, sc.Household.Currency)
		}
	}
	if len(fields) > 0 {
		form.InvoiceRef = invoice
		form.Notes = notes
		s.renderBillForm(w, r, sc, form, fields, "Check the bill and try again.", http.StatusUnprocessableEntity)
		return
	}

	shares := make([]store.NewShare, len(portions))
	for i, p := range portions {
		shares[i] = store.NewShare{ParticipantID: p.UserID, AmountCents: p.AmountCents, Weight: p.Weight}
	}
	_, err = s.store.CreateBill(r.Context(), store.NewBill{
		HouseholdID:         sc.Household.ID,
		UtilityID:           form.UtilityID,
		PeriodStart:         start,
		PeriodEnd:           end,
		DueDate:             due,
		AmountCents:         total,
		PaidByParticipantID: form.PaidBy,
		InvoiceRef:          invoice,
		Notes:               notes,
		SplitMethod:         form.SplitMethod,
		CreatedBy:           sc.User.ID,
		Shares:              shares,
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.redirect(w, r, "/bills?notice=bill")
}

func (s *Server) showBill(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.mustScope(w, r)
	if !ok {
		return
	}
	s.renderBill(w, r, sc, r.PathValue("billID"), "", http.StatusOK)
}

func (s *Server) postPayment(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.mustScope(w, r)
	if !ok {
		return
	}
	billID := r.PathValue("billID")
	userID := r.FormValue("user_id")
	note, noteOK := cleanNotes(r.FormValue("note"), 200)
	amount, amountErr := money.Parse(r.FormValue("amount"))
	if !token.ValidID(billID) || !token.ValidID(userID) || !noteOK || amountErr != nil || amount <= 0 {
		s.renderBill(w, r, sc, billID, "Enter a payment amount.", http.StatusUnprocessableEntity)
		return
	}
	err := s.store.AddPayment(r.Context(), sc.Household.ID, billID, userID, amount, note, sc.User.ID)
	switch {
	case err == nil:
		s.redirect(w, r, "/bills/"+billID+"?notice=payment")
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, store.ErrPaymentTooLarge):
		s.renderBill(w, r, sc, billID, "That payment is more than the remaining share.", http.StatusUnprocessableEntity)
	case errors.Is(err, store.ErrProviderUnpaid):
		s.renderBill(w, r, sc, billID, "Record who paid the provider before reimbursing anyone.", http.StatusUnprocessableEntity)
	case errors.Is(err, store.ErrInvalid):
		s.renderBill(w, r, sc, billID, "That payment can't be recorded.", http.StatusUnprocessableEntity)
	default:
		s.fail(w, r, err)
	}
}

func (s *Server) postProviderPaid(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.mustScope(w, r)
	if !ok {
		return
	}
	billID := r.PathValue("billID")
	payerID := r.FormValue("payer_id")
	if !token.ValidID(billID) || !token.ValidID(payerID) {
		http.NotFound(w, r)
		return
	}
	err := s.store.MarkProviderPaid(r.Context(), sc.Household.ID, billID, payerID)
	switch {
	case err == nil:
		s.redirect(w, r, "/bills/"+billID+"?notice=provider")
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, store.ErrInvalid):
		s.renderBill(w, r, sc, billID, "Choose someone who is on this bill.", http.StatusUnprocessableEntity)
	default:
		s.fail(w, r, err)
	}
}

func (s *Server) postDeleteBill(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.mustScope(w, r)
	if !ok {
		return
	}
	billID := r.PathValue("billID")
	if !token.ValidID(billID) {
		http.NotFound(w, r)
		return
	}
	bill, err := s.store.Bill(r.Context(), sc.Household.ID, billID)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if bill.CreatedBy != sc.User.ID && !sc.owner() {
		s.renderBill(w, r, sc, billID, "Only the person who recorded this bill, or an owner, can delete it.", http.StatusUnprocessableEntity)
		return
	}
	err = s.store.DeleteBill(r.Context(), sc.Household.ID, billID)
	switch {
	case err == nil:
		s.redirect(w, r, "/bills?notice=deleted")
	case errors.Is(err, store.ErrHasPayments):
		s.renderBill(w, r, sc, billID, "Payments are already recorded, so this bill stays in the ledger.", http.StatusUnprocessableEntity)
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	default:
		s.fail(w, r, err)
	}
}

func (s *Server) renderBillForm(w http.ResponseWriter, r *http.Request, sc scope, form BillForm, fields map[string]string, errMsg string, status int) {
	utils, err := s.store.Utilities(r.Context(), sc.Household.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if len(utils) == 0 && status == http.StatusOK {
		s.redirect(w, r, "/utilities?notice=need-utility")
		return
	}
	members, err := s.store.Members(r.Context(), sc.Household.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	people := toPeople(members, sc.User.ID)
	if err := s.resolveIncludes(r, sc, &form, people, false); err != nil {
		s.fail(w, r, err)
		return
	}
	people = applyIncludeFlags(people, form)
	if form.Weights == nil {
		form.Weights = map[string]string{}
	}
	if form.Amounts == nil {
		form.Amounts = map[string]string{}
	}
	if form.SplitMethod == "" {
		form.SplitMethod = "equal"
	}
	s.applySplitPreview(&form, people, sc.Household.Currency)
	v := s.shell(r, sc, "Record a bill", "bills")
	v.Content = "bill-new"
	v.Utilities = utilityOptions(utils)
	v.People = people
	v.Form = form
	v.Fields = fields
	v.Error = errMsg
	s.render(w, status, "app", v)
}

func (s *Server) renderBill(w http.ResponseWriter, r *http.Request, sc scope, billID, errMsg string, status int) {
	if !token.ValidID(billID) {
		http.NotFound(w, r)
		return
	}
	bill, err := s.store.Bill(r.Context(), sc.Household.ID, billID)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	members, err := s.store.Members(r.Context(), sc.Household.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	noPayments := true
	for _, sh := range bill.Shares {
		if sh.PaidCents > 0 {
			noPayments = false
			break
		}
	}
	canDelete := noPayments && (bill.CreatedBy == sc.User.ID || sc.owner())
	v := s.shell(r, sc, bill.UtilityName, "bills")
	v.Content = "bill-show"
	v.Error = errMsg
	v.People = toPeople(members, sc.User.ID)
	v.Bill = presentBill(bill, sc.SelfID, sc.Household.Currency, canDelete)
	s.render(w, status, "app", v)
}

func (s *Server) applySplitPreview(form *BillForm, people []Person, currency string) {
	form.Preview = nil
	form.PreviewError = ""
	if strings.TrimSpace(form.Amount) == "" {
		return
	}
	total, err := money.Parse(form.Amount)
	if err != nil || total <= 0 {
		form.PreviewError = "Enter an amount to preview the split."
		return
	}
	portions, err := portionsFromForm(*form, people, total)
	if err != nil {
		form.PreviewError = userSplitError(err, currency)
		return
	}
	names := map[string]string{}
	for _, p := range people {
		names[p.ID] = p.Name
	}
	for _, p := range portions {
		form.Preview = append(form.Preview, SharePreview{
			Name:   names[p.UserID],
			Amount: money.Format(p.AmountCents, currency),
		})
	}
	sort.Slice(form.Preview, func(i, j int) bool {
		return form.Preview[i].Name < form.Preview[j].Name
	})
}

func readBillForm(r *http.Request, people []Person) BillForm {
	form := BillForm{
		UtilityID:   r.FormValue("utility_id"),
		PeriodStart: r.FormValue("period_start"),
		PeriodEnd:   r.FormValue("period_end"),
		DueDate:     r.FormValue("due_date"),
		Amount:      r.FormValue("amount"),
		PaidBy:      r.FormValue("paid_by"),
		InvoiceRef:  r.FormValue("invoice_ref"),
		Notes:       r.FormValue("notes"),
		SplitMethod: r.FormValue("split_method"),
		Weights:     map[string]string{},
		Amounts:     map[string]string{},
	}
	if form.SplitMethod == "" {
		form.SplitMethod = "equal"
	}
	form.IncludesSent = r.FormValue("includes_sent") == "1"
	form.AppliedUtility = r.FormValue("applied_utility")
	form.Include = map[string]bool{}
	for _, p := range people {
		if v := r.FormValue("weight_" + p.ID); v != "" {
			form.Weights[p.ID] = v
		}
		if v := r.FormValue("amount_" + p.ID); v != "" {
			form.Amounts[p.ID] = v
		}
		if r.FormValue("include_"+p.ID) == "1" {
			form.Include[p.ID] = true
		}
	}
	return form
}

func (s *Server) resolveIncludes(r *http.Request, sc scope, form *BillForm, people []Person, utilityChanged bool) error {
	exempt := map[string]struct{}{}
	if token.ValidID(form.UtilityID) {
		var err error
		exempt, err = s.store.ExemptionSet(r.Context(), sc.Household.ID, form.UtilityID)
		if err != nil {
			return err
		}
	}
	reapply := !form.IncludesSent || (utilityChanged && form.AppliedUtility != form.UtilityID)
	if reapply {
		form.Include = map[string]bool{}
		for _, p := range people {
			_, skip := exempt[p.ID]
			form.Include[p.ID] = !skip
		}
		form.IncludesSent = true
		form.AppliedUtility = form.UtilityID
	}
	return nil
}

func applyIncludeFlags(people []Person, form BillForm) []Person {
	out := make([]Person, len(people))
	for i, p := range people {
		p.Included = !form.IncludesSent || form.Include[p.ID]
		out[i] = p
	}
	return out
}

func includedPeople(form BillForm, people []Person) []Person {
	if !form.IncludesSent {
		return people
	}
	out := make([]Person, 0, len(people))
	for _, p := range people {
		if form.Include[p.ID] {
			out = append(out, p)
		}
	}
	return out
}

func defaultBillForm() BillForm {
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	end := start.AddDate(0, 1, -1)
	due := now.AddDate(0, 0, 10)
	return BillForm{
		PeriodStart: start.Format("2006-01-02"),
		PeriodEnd:   end.Format("2006-01-02"),
		DueDate:     due.Format("2006-01-02"),
		SplitMethod: "equal",
		Weights:     map[string]string{},
		Amounts:     map[string]string{},
	}
}

func portionsFromForm(form BillForm, people []Person, total int64) ([]billing.Portion, error) {
	people = includedPeople(form, people)
	switch form.SplitMethod {
	case "equal":
		ids := make([]string, len(people))
		for i, p := range people {
			ids[i] = p.ID
		}
		return billing.SplitEqual(total, ids)
	case "weights":
		parts := make([]billing.Portion, len(people))
		for i, p := range people {
			raw := strings.TrimSpace(form.Weights[p.ID])
			if raw == "" {
				raw = "1"
			}
			weight, err := strconv.Atoi(raw)
			if err != nil || weight < 0 || weight > 100 {
				return nil, errors.New("enter weights from 0 to 100")
			}
			parts[i] = billing.Portion{UserID: p.ID, Weight: weight}
		}
		return billing.SplitWeighted(total, parts)
	case "amounts":
		parts := make([]billing.Portion, len(people))
		for i, p := range people {
			cents, err := money.ParseOptional(form.Amounts[p.ID])
			if err != nil {
				return nil, err
			}
			parts[i] = billing.Portion{UserID: p.ID, AmountCents: cents}
		}
		return billing.SplitFixed(total, parts)
	default:
		return nil, errors.New("choose how to split the bill")
	}
}

func userSplitError(err error, currency string) string {
	var mismatch billing.SumMismatchError
	if errors.As(err, &mismatch) {
		return fmt.Sprintf("Shares add up to %s. The bill is %s.", money.Format(mismatch.Got, currency), money.Format(mismatch.Want, currency))
	}
	return err.Error()
}

func normalizeFilter(status string) string {
	switch status {
	case "unpaid", "open", "settled":
		return status
	default:
		return "all"
	}
}
