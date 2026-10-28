package web

import (
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"hearth/internal/store"
	"hearth/internal/token"
)

var (
	dummyOnce     sync.Once
	dummyHash     []byte
	errOutOfRange = errors.New("date is out of range")
)

func dummyPasswordHash() []byte {
	dummyOnce.Do(func() {
		hash, err := bcrypt.GenerateFromPassword([]byte("not-a-real-password"), bcryptCost)
		if err != nil {
			panic(err)
		}
		dummyHash = hash
	})
	return dummyHash
}

func (s *Server) getLogin(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.optionalUser(w, r); ok {
		if raw := r.URL.Query().Get("invite"); token.Valid(raw) {
			s.redirect(w, r, "/invites/"+raw)
			return
		}
		s.redirect(w, r, "/")
		return
	}
	v := s.authBase(r, "Log in")
	v.Content = "login"
	s.attachInvite(r, &v)
	s.prepareOAuthForm(w, r, &v)
	s.render(w, http.StatusOK, "auth", v)
}

func (s *Server) getRegister(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.optionalUser(w, r); ok {
		if raw := r.URL.Query().Get("invite"); token.Valid(raw) {
			s.redirect(w, r, "/invites/"+raw)
			return
		}
		s.redirect(w, r, "/")
		return
	}
	v := s.authBase(r, "Create account")
	v.Content = "register"
	s.attachInvite(r, &v)
	if v.Token != "" {
		v.Email = v.InviteEmail
	}
	s.prepareOAuthForm(w, r, &v)
	s.render(w, http.StatusOK, "auth", v)
}

func (s *Server) postRegister(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.allow("register:" + clientIP(r, s.cfg.TrustProxy)) {
		s.authError(w, r, "register", "Too many attempts. Wait a few minutes and try again.", http.StatusTooManyRequests)
		return
	}
	v := s.authBase(r, "Create account")
	v.Content = "register"
	s.attachInvite(r, &v)
	name, nameOK := cleanName(r.FormValue("name"), 80)
	email, emailOK := cleanEmail(r.FormValue("email"))
	password := r.FormValue("password")
	v.Name = strings.TrimSpace(r.FormValue("name"))
	v.Email = strings.TrimSpace(r.FormValue("email"))
	switch {
	case !nameOK:
		v.Error = "Enter your name."
	case !emailOK:
		v.Error = "Enter a valid email address."
	case v.Token != "" && !strings.EqualFold(email, v.InviteEmail):
		v.Error = "Use the invited email address."
		v.Email = v.InviteEmail
	case len(password) < 10:
		v.Error = "Use a password of at least 10 characters."
	case len(password) > 72:
		v.Error = "Use a password of at most 72 characters."
	}
	if v.Error != "" {
		s.prepareOAuthForm(w, r, &v)
		s.render(w, http.StatusUnprocessableEntity, "auth", v)
		return
	}
	if !s.oauthStateOK(r) {
		v.Error = "The form expired. Refresh the page and try again."
		s.prepareOAuthForm(w, r, &v)
		s.render(w, http.StatusForbidden, "auth", v)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	user, err := s.store.CreateUser(r.Context(), name, email, string(hash))
	if errors.Is(err, store.ErrConflict) {
		v.Error = "An account with that email already exists. Log in instead."
		s.prepareOAuthForm(w, r, &v)
		s.render(w, http.StatusUnprocessableEntity, "auth", v)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.beginGrant(w, r, user, v.Token)
}

func (s *Server) postLogout(w http.ResponseWriter, r *http.Request) {
	if acc := currentAccess(r.Context()); acc.RefreshRaw != "" {
		if err := s.store.RevokeRefresh(r.Context(), acc.RefreshRaw); err != nil {
			s.log.Error("logout", "err", err)
		}
	}
	s.clearCookie(w, accessCookie)
	s.clearCookie(w, refreshCookie)
	s.redirect(w, r, "/login")
}

func (s *Server) getInvite(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("token")
	if !token.Valid(raw) {
		s.renderInviteProblem(w, r, "", "That invite link is not valid.")
		return
	}
	inv, err := s.store.InviteByToken(r.Context(), raw)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrExpired) || errors.Is(err, store.ErrUsed) {
			s.renderInviteProblem(w, r, inviteProblem(err), "")
			return
		}
		s.fail(w, r, err)
		return
	}
	acc, user, ok := s.optionalUser(w, r)
	if !ok {
		v := s.authBase(r, "Join household")
		v.Content = "invite"
		v.Token = raw
		v.InviteHousehold = inv.HouseholdName
		v.InviteEmail = inv.Email
		s.render(w, http.StatusOK, "auth", v)
		return
	}
	sc, err := s.scopeFor(w, r, user, acc)
	if err != nil && !errors.Is(err, errNoHousehold) {
		s.fail(w, r, err)
		return
	}
	v := s.shell(r, sc, "Join household", "")
	v.Content = "invite"
	v.Token = raw
	v.InviteHousehold = inv.HouseholdName
	v.InviteEmail = inv.Email
	v.LoggedIn = true
	v.EmailMismatch = !strings.EqualFold(user.Email, inv.Email)
	if !v.EmailMismatch {
		_, memberErr := s.store.Membership(r.Context(), inv.HouseholdID, user.ID)
		switch {
		case memberErr == nil:
			v.AlreadyMember = true
		case errors.Is(memberErr, store.ErrNotFound):
		default:
			s.fail(w, r, memberErr)
			return
		}
	}
	// shell reads CSRF from context, which the public invite route does not set via auth.
	v.CSRF = csrfFrom(r.Context())
	s.render(w, http.StatusOK, "app", v)
}

func (s *Server) postAcceptInvite(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("token")
	if !token.Valid(raw) {
		http.NotFound(w, r)
		return
	}
	user := currentUser(r.Context())
	acc := currentAccess(r.Context())
	inv, err := s.store.InviteByToken(r.Context(), raw)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrExpired) || errors.Is(err, store.ErrUsed) {
			s.renderInviteProblem(w, r, inviteProblem(err), "")
			return
		}
		s.fail(w, r, err)
		return
	}
	if !strings.EqualFold(user.Email, inv.Email) {
		sc, scopeErr := s.scopeFor(w, r, user, acc)
		if scopeErr != nil && !errors.Is(scopeErr, errNoHousehold) {
			s.fail(w, r, scopeErr)
			return
		}
		v := s.shell(r, sc, "Join household", "")
		v.Content = "invite"
		v.Token = raw
		v.InviteHousehold = inv.HouseholdName
		v.InviteEmail = inv.Email
		v.LoggedIn = true
		v.EmailMismatch = true
		v.Error = "This invite was sent to a different email."
		s.render(w, http.StatusUnprocessableEntity, "app", v)
		return
	}
	householdID, err := s.store.AcceptInvite(r.Context(), raw, user.ID, user.Email)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrExpired) || errors.Is(err, store.ErrUsed) || errors.Is(err, store.ErrEmailMismatch) {
			s.renderInviteProblem(w, r, inviteProblem(err), "")
			return
		}
		s.fail(w, r, err)
		return
	}
	if err := s.activateHousehold(w, r, acc, householdID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.redirect(w, r, "/?notice=joined")
}

func (s *Server) authBase(r *http.Request, title string) View {
	return View{
		Title:  title,
		CSRF:   csrfFrom(r.Context()),
		Year:   time.Now().Year(),
		Fields: map[string]string{},
	}
}

func (s *Server) authError(w http.ResponseWriter, r *http.Request, content, message string, status int) {
	v := s.authBase(r, "Hearth")
	v.Content = content
	v.Error = message
	v.Email = strings.TrimSpace(r.FormValue("email"))
	s.attachInvite(r, &v)
	s.render(w, status, "auth", v)
}

func (s *Server) attachInvite(r *http.Request, v *View) {
	raw, email, household, problem, err := s.invitePreview(r)
	if err != nil {
		s.log.Error("invite preview", "err", err)
		v.Error = "Something went wrong loading that invite."
		return
	}
	if problem != "" && v.Error == "" {
		v.Error = problem
	}
	v.Token = raw
	v.InviteEmail = email
	v.InviteHousehold = household
}

func (s *Server) invitePreview(r *http.Request) (raw, email, household, problem string, err error) {
	raw = strings.TrimSpace(r.URL.Query().Get("invite"))
	if form := strings.TrimSpace(r.FormValue("invite")); form != "" {
		raw = form
	}
	if raw == "" {
		return "", "", "", "", nil
	}
	if !token.Valid(raw) {
		return "", "", "", "That invite link is not valid.", nil
	}
	inv, err := s.store.InviteByToken(r.Context(), raw)
	if err == nil {
		return raw, inv.Email, inv.HouseholdName, "", nil
	}
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrExpired) || errors.Is(err, store.ErrUsed) {
		return "", "", "", inviteProblem(err), nil
	}
	return "", "", "", "", err
}

func (s *Server) renderInviteProblem(w http.ResponseWriter, r *http.Request, problem, fallback string) {
	if problem == "" {
		problem = fallback
	}
	if problem == "" {
		problem = "That invite link is not valid."
	}
	if acc, user, ok := s.optionalUser(w, r); ok {
		sc, err := s.scopeFor(w, r, user, acc)
		if err != nil && !errors.Is(err, errNoHousehold) {
			s.fail(w, r, err)
			return
		}
		v := s.shell(r, sc, "Invite", "")
		v.Content = "invite"
		v.LoggedIn = true
		v.Error = problem
		v.CSRF = csrfFrom(r.Context())
		s.render(w, http.StatusOK, "app", v)
		return
	}
	v := s.authBase(r, "Invite")
	v.Content = "invite"
	v.Error = problem
	s.render(w, http.StatusOK, "auth", v)
}

func inviteProblem(err error) string {
	switch {
	case errors.Is(err, store.ErrExpired):
		return "That invite expired. Ask for a new link."
	case errors.Is(err, store.ErrUsed):
		return "That invite was already used."
	default:
		return "That invite link is not valid."
	}
}
