package web

import (
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"hearth/internal/billing"
	"hearth/internal/money"
	"hearth/internal/store"
	"hearth/internal/token"
)

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	sc, err := s.scope(w, r)
	if err != nil && !errors.Is(err, errNoHousehold) {
		s.fail(w, r, err)
		return
	}
	if sc.Household.ID == "" {
		v := s.shell(r, sc, "Create household", "ledger")
		v.Content = "onboarding"
		v.Currencies = money.Currencies()
		s.render(w, http.StatusOK, "app", v)
		return
	}
	bills, members, utils, ok := s.householdLists(w, r, sc.Household.ID)
	if !ok {
		return
	}
	v := s.shell(r, sc, sc.Household.Name, "ledger")
	v.Content = "dashboard"
	lines, youOwe, owed := buildBalances(sc.SelfID, sc.Household.Currency, bills)
	v.BalanceLines = lines
	v.YouOwe = money.Format(youOwe, sc.Household.Currency)
	v.OwedToYou = money.Format(owed, sc.Household.Currency)
	var attention []store.Bill
	for _, bill := range bills {
		if bill.Status() != billing.StatusSettled {
			attention = append(attention, bill)
		}
	}
	v.Attention = presentBillRows(attention, sc.SelfID, sc.Household.Currency)
	v.AttentionCount = len(attention)
	v.ShowSetup = len(bills) == 0
	v.MemberCount = len(members)
	v.HasUtilities = len(utils) > 0
	v.SettledUp = len(bills) > 0 && len(attention) == 0
	if len(bills) > 0 {
		v.Chart = normalizeChart(r.URL.Query().Get("chart"))
		v.ChartJSON = spendingChart(bills, v.Chart, sc.Household.Currency)
		latest := append([]store.Bill(nil), bills...)
		sort.Slice(latest, func(i, j int) bool {
			return latest[i].CreatedAt.After(latest[j].CreatedAt)
		})
		if len(latest) > 8 {
			latest = latest[:8]
		}
		v.Latest = presentBillRows(latest, sc.SelfID, sc.Household.Currency)
	}
	s.render(w, http.StatusOK, "app", v)
}

func (s *Server) postHousehold(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	acc := currentAccess(r.Context())
	name, nameOK := cleanName(r.FormValue("name"), 80)
	currency := r.FormValue("currency")
	if !nameOK || !money.Valid(currency) {
		s.householdFormError(w, r)
		return
	}
	household, err := s.store.CreateHousehold(r.Context(), name, currency, user.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.activateHousehold(w, r, acc, household.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.redirect(w, r, "/?notice=household")
}

func (s *Server) postSwitchHousehold(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("household_id")
	if !token.ValidID(id) {
		http.NotFound(w, r)
		return
	}
	user := currentUser(r.Context())
	if _, err := s.store.Membership(r.Context(), id, user.ID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "You can't open that household.", http.StatusForbidden)
			return
		}
		s.fail(w, r, err)
		return
	}
	if err := s.activateHousehold(w, r, currentAccess(r.Context()), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.redirect(w, r, "/")
}

func (s *Server) postHouseholdSettings(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.mustScope(w, r)
	if !ok {
		return
	}
	if !sc.owner() {
		s.renderMembers(w, r, sc, "Only an owner can rename the household.", http.StatusUnprocessableEntity, "", "")
		return
	}
	name, nameOK := cleanName(r.FormValue("name"), 80)
	if !nameOK {
		s.renderMembers(w, r, sc, "Enter a household name.", http.StatusUnprocessableEntity, "", "")
		return
	}
	if err := s.store.RenameHousehold(r.Context(), sc.Household.ID, name); err != nil {
		s.fail(w, r, err)
		return
	}
	s.redirect(w, r, "/members?notice=renamed")
}

func (s *Server) members(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.mustScope(w, r)
	if !ok {
		return
	}
	s.renderMembers(w, r, sc, "", http.StatusOK, "", "")
}

func (s *Server) postInvite(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.mustScope(w, r)
	if !ok {
		return
	}
	if !sc.owner() {
		s.renderMembers(w, r, sc, "Only an owner can invite people.", http.StatusUnprocessableEntity, "", "")
		return
	}
	email, emailOK := cleanEmail(r.FormValue("email"))
	if !emailOK {
		s.renderMembers(w, r, sc, "Enter the email they will use to join.", http.StatusUnprocessableEntity, "", "")
		return
	}
	exists, err := s.store.HasMemberEmail(r.Context(), sc.Household.ID, email)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if exists {
		s.renderMembers(w, r, sc, "That person is already in the household.", http.StatusUnprocessableEntity, "", "")
		return
	}
	raw, err := s.store.CreateInvite(r.Context(), sc.Household.ID, email, sc.User.ID, inviteTTL)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.setCookie(w, inviteCookie, raw, 300, true)
	s.redirect(w, r, "/members?notice=invite")
}

func (s *Server) postParticipant(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.mustScope(w, r)
	if !ok {
		return
	}
	name, nameOK := cleanName(r.FormValue("name"), 80)
	emailRaw := strings.TrimSpace(r.FormValue("email"))
	email := ""
	emailOK := true
	if emailRaw != "" {
		email, emailOK = cleanEmail(emailRaw)
	}
	if !nameOK || !emailOK {
		s.renderMembers(w, r, sc, "Enter a name. The email can be blank.", http.StatusUnprocessableEntity, "", "")
		return
	}
	if _, err := s.store.AddParticipant(r.Context(), sc.Household.ID, name, email); err != nil {
		if errors.Is(err, store.ErrConflict) {
			s.renderMembers(w, r, sc, "That person is already in the household.", http.StatusUnprocessableEntity, "", "")
			return
		}
		s.fail(w, r, err)
		return
	}
	s.redirect(w, r, "/members?notice=person")
}

func (s *Server) postRevokeInvite(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.mustScope(w, r)
	if !ok {
		return
	}
	if !sc.owner() {
		s.renderMembers(w, r, sc, "Only an owner can revoke an invite.", http.StatusUnprocessableEntity, "", "")
		return
	}
	id := r.PathValue("inviteID")
	if !token.ValidID(id) {
		http.NotFound(w, r)
		return
	}
	if err := s.store.RevokeInvite(r.Context(), sc.Household.ID, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		s.fail(w, r, err)
		return
	}
	s.redirect(w, r, "/members?notice=revoked")
}

func (s *Server) postRemoveMember(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.mustScope(w, r)
	if !ok {
		return
	}
	if !sc.owner() {
		s.renderMembers(w, r, sc, "Only an owner can remove someone.", http.StatusUnprocessableEntity, "", "")
		return
	}
	participantID := r.PathValue("participantID")
	if !token.ValidID(participantID) {
		http.NotFound(w, r)
		return
	}
	if participantID == sc.SelfID {
		s.renderMembers(w, r, sc, "You can't remove yourself.", http.StatusUnprocessableEntity, "", "")
		return
	}
	err := s.store.RemoveParticipant(r.Context(), sc.Household.ID, participantID)
	switch {
	case err == nil:
		s.redirect(w, r, "/members?notice=removed")
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, store.ErrLastOwner):
		s.renderMembers(w, r, sc, "The household still needs an owner.", http.StatusUnprocessableEntity, "", "")
	case errors.Is(err, store.ErrOpenBills):
		s.renderMembers(w, r, sc, "Settle or delete open bills before removing someone.", http.StatusUnprocessableEntity, "", "")
	default:
		s.fail(w, r, err)
	}
}

func (s *Server) utilities(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.mustScope(w, r)
	if !ok {
		return
	}
	s.renderUtilities(w, r, sc, "", http.StatusOK)
}

func (s *Server) postUtility(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.mustScope(w, r)
	if !ok {
		return
	}
	name, nameOK := cleanName(r.FormValue("name"), 80)
	provider, providerOK := cleanOptional(r.FormValue("provider"), 80)
	category := r.FormValue("category")
	if !nameOK || !providerOK || !validCategory(category) {
		s.renderUtilities(w, r, sc, "Enter a utility name and category.", http.StatusUnprocessableEntity)
		return
	}
	members, err := s.store.Members(r.Context(), sc.Household.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	created, err := s.store.CreateUtility(r.Context(), sc.Household.ID, name, provider, category)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.store.SetExemptions(r.Context(), sc.Household.ID, created.ID, exemptIDs(r, toPeople(members, sc.User.ID))); err != nil {
		s.fail(w, r, err)
		return
	}
	s.redirect(w, r, "/utilities?notice=utility")
}

func (s *Server) editUtility(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.mustScope(w, r)
	if !ok {
		return
	}
	if !sc.owner() {
		http.Error(w, "Only the household owner can edit a utility.", http.StatusForbidden)
		return
	}
	utilityID := r.PathValue("utilityID")
	if !token.ValidID(utilityID) {
		http.NotFound(w, r)
		return
	}
	utils, err := s.store.Utilities(r.Context(), sc.Household.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for _, utility := range utils {
		if utility.ID == utilityID {
			s.renderUtilityEdit(w, r, sc, utility.Name, utility.Provider, utility.Category, "", http.StatusOK)
			return
		}
	}
	http.NotFound(w, r)
}

func (s *Server) postUpdateUtility(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.mustScope(w, r)
	if !ok {
		return
	}
	if !sc.owner() {
		http.Error(w, "Only the household owner can edit a utility.", http.StatusForbidden)
		return
	}
	utilityID := r.PathValue("utilityID")
	if !token.ValidID(utilityID) {
		http.NotFound(w, r)
		return
	}
	name, nameOK := cleanName(r.FormValue("name"), 80)
	provider, providerOK := cleanOptional(r.FormValue("provider"), 80)
	category := r.FormValue("category")
	if !nameOK || !providerOK || !validCategory(category) {
		s.renderUtilityEdit(w, r, sc, strings.TrimSpace(r.FormValue("name")), strings.TrimSpace(r.FormValue("provider")), category, "Enter a utility name and category.", http.StatusUnprocessableEntity)
		return
	}
	err := s.store.UpdateUtility(r.Context(), sc.Household.ID, utilityID, name, provider, category)
	switch {
	case err == nil:
		s.redirect(w, r, "/utilities?notice=utility-edited")
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	default:
		s.fail(w, r, err)
	}
}

func (s *Server) renderUtilityEdit(w http.ResponseWriter, r *http.Request, sc scope, name, provider, category, errMsg string, status int) {
	v := s.shell(r, sc, "Edit utility", "utilities")
	v.Content = "utility-edit"
	v.UtilityID = r.PathValue("utilityID")
	v.Name = name
	v.Provider = provider
	v.SelectedCategory = category
	v.Categories = categories()
	v.Error = errMsg
	s.render(w, status, "app", v)
}

func (s *Server) postExemptions(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.mustScope(w, r)
	if !ok {
		return
	}
	utilityID := r.PathValue("utilityID")
	if !token.ValidID(utilityID) {
		http.NotFound(w, r)
		return
	}
	members, err := s.store.Members(r.Context(), sc.Household.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.store.SetExemptions(r.Context(), sc.Household.ID, utilityID, exemptIDs(r, toPeople(members, sc.User.ID))); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if errors.Is(err, store.ErrInvalid) {
			if r.Header.Get("HX-Request") == "true" {
				http.Error(w, "Choose people from this household.", http.StatusUnprocessableEntity)
				return
			}
			s.renderUtilities(w, r, sc, "Choose people from this household.", http.StatusUnprocessableEntity)
			return
		}
		s.fail(w, r, err)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		w.WriteHeader(http.StatusOK)
		return
	}
	s.redirect(w, r, "/utilities?notice=exemptions")
}

func (s *Server) postRepeat(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.mustScope(w, r)
	if !ok {
		return
	}
	utilityID := r.PathValue("utilityID")
	if !token.ValidID(utilityID) {
		http.NotFound(w, r)
		return
	}
	err := s.store.SetRepeats(r.Context(), sc.Household.ID, utilityID, r.FormValue("repeats") == "1")
	switch {
	case err == nil:
		if r.Header.Get("HX-Request") == "true" {
			w.WriteHeader(http.StatusOK)
			return
		}
		s.redirect(w, r, "/utilities?notice=repeat")
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	default:
		s.fail(w, r, err)
	}
}

func exemptIDs(r *http.Request, people []Person) []string {
	if r.FormValue("includes_sent") != "1" {
		return nil
	}
	var ids []string
	for _, p := range people {
		if r.FormValue("include_"+p.ID) != "1" {
			ids = append(ids, p.ID)
		}
	}
	return ids
}

func (s *Server) householdLists(w http.ResponseWriter, r *http.Request, householdID string) ([]store.Bill, []store.Member, []store.Utility, bool) {
	if err := s.store.EnsureRecurring(r.Context(), householdID, time.Now()); err != nil {
		s.fail(w, r, err)
		return nil, nil, nil, false
	}
	bills, err := s.store.Bills(r.Context(), householdID)
	if err != nil {
		s.fail(w, r, err)
		return nil, nil, nil, false
	}
	members, err := s.store.Members(r.Context(), householdID)
	if err != nil {
		s.fail(w, r, err)
		return nil, nil, nil, false
	}
	utils, err := s.store.Utilities(r.Context(), householdID)
	if err != nil {
		s.fail(w, r, err)
		return nil, nil, nil, false
	}
	return bills, members, utils, true
}

func (s *Server) householdFormError(w http.ResponseWriter, r *http.Request) {
	sc, err := s.scope(w, r)
	if err != nil && !errors.Is(err, errNoHousehold) {
		s.fail(w, r, err)
		return
	}
	if r.FormValue("from") == "members" && sc.Household.ID != "" {
		s.renderMembers(w, r, sc, "Enter a household name and currency.", http.StatusUnprocessableEntity, r.FormValue("name"), r.FormValue("currency"))
		return
	}
	v := s.shell(r, sc, "Create household", "ledger")
	v.Content = "onboarding"
	v.Error = "Enter a household name and currency."
	v.DraftName = strings.TrimSpace(r.FormValue("name"))
	v.DraftCurrency = r.FormValue("currency")
	if !money.Valid(v.DraftCurrency) {
		v.DraftCurrency = "USD"
	}
	v.Currencies = money.Currencies()
	s.render(w, http.StatusUnprocessableEntity, "app", v)
}

func (s *Server) renderMembers(w http.ResponseWriter, r *http.Request, sc scope, errMsg string, status int, draftName, draftCurrency string) {
	members, err := s.store.Members(r.Context(), sc.Household.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	invites, err := s.store.PendingInvites(r.Context(), sc.Household.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v := s.shell(r, sc, "Members", "members")
	v.Content = "members"
	v.Error = errMsg
	v.MemberRows = toPeople(members, sc.User.ID)
	if err := s.store.EnsureRecurring(r.Context(), sc.Household.ID, time.Now()); err != nil {
		s.fail(w, r, err)
		return
	}
	houseBills, err := s.store.Bills(r.Context(), sc.Household.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	contributed := contributions(houseBills)
	var total int64
	for _, cents := range contributed {
		if cents > 0 {
			total += cents
		}
	}
	for i := range v.MemberRows {
		v.MemberRows[i].Contributed = contributionLabel(contributed[v.MemberRows[i].ID], total, sc.Household.Currency)
	}
	v.Currencies = money.Currencies()
	v.DraftName = strings.TrimSpace(draftName)
	v.DraftCurrency = draftCurrency
	if !money.Valid(v.DraftCurrency) {
		v.DraftCurrency = "USD"
	}
	for _, inv := range invites {
		v.InviteRows = append(v.InviteRows, InviteRow{
			ID:      inv.ID,
			Email:   inv.Email,
			Expires: inv.ExpiresAt.In(time.Local).Format("Jan 2, 2006"),
		})
	}
	if c, err := r.Cookie(inviteCookie); err == nil && token.Valid(c.Value) {
		inv, err := s.store.InviteByToken(r.Context(), c.Value)
		if err == nil && inv.HouseholdID == sc.Household.ID {
			v.InviteURL = s.absolute(r, "/invites/"+c.Value)
		}
	}
	s.render(w, status, "app", v)
}

func (s *Server) renderUtilities(w http.ResponseWriter, r *http.Request, sc scope, errMsg string, status int) {
	utils, err := s.store.Utilities(r.Context(), sc.Household.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v := s.shell(r, sc, "Utilities", "utilities")
	v.Content = "utilities"
	v.Error = errMsg
	v.Categories = categories()
	v.Name = strings.TrimSpace(r.FormValue("name"))
	v.Provider = strings.TrimSpace(r.FormValue("provider"))
	v.SelectedCategory = r.FormValue("category")
	members, err := s.store.Members(r.Context(), sc.Household.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v.People = toPeople(members, sc.User.ID)
	for _, u := range utils {
		provider := u.Provider
		if provider == "" {
			provider = "—"
		}
		exempt, err := s.store.ExemptionSet(r.Context(), sc.Household.ID, u.ID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		rowPeople := make([]Person, len(v.People))
		for i, p := range v.People {
			_, exempted := exempt[p.ID]
			p.Included = !exempted
			rowPeople[i] = p
		}
		v.UtilityRows = append(v.UtilityRows, UtilityRow{
			ID:       u.ID,
			Name:     u.Name,
			Provider: provider,
			Category: categoryLabel(u.Category),
			Repeats:  u.Repeats,
			People:   rowPeople,
		})
	}
	s.render(w, status, "app", v)
}
