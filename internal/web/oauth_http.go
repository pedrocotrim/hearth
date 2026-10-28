package web

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/crypto/bcrypt"

	"hearth/internal/oauth"
	"hearth/internal/store"
	"hearth/internal/token"
)

func (s *Server) writeAccessCookie(w http.ResponseWriter, userID, householdID string) error {
	now := time.Now()
	jti, err := token.New()
	if err != nil {
		return err
	}
	raw, err := s.signer.Sign(oauth.Claims{
		Sub:       userID,
		Iss:       oauth.Issuer,
		Aud:       oauth.ClientID,
		Exp:       now.Add(accessTTL).Unix(),
		Iat:       now.Unix(),
		JTI:       jti,
		Household: householdID,
	})
	if err != nil {
		return err
	}
	s.setCookie(w, accessCookie, raw, int(accessTTL.Seconds()), true)
	return nil
}

func (s *Server) activateHousehold(w http.ResponseWriter, r *http.Request, acc store.Access, householdID string) error {
	if acc.RefreshRaw == "" {
		return s.writeAccessCookie(w, acc.UserID, householdID)
	}
	if err := s.store.SetRefreshHousehold(r.Context(), acc.RefreshRaw, householdID); err != nil {
		return err
	}
	return s.writeAccessCookie(w, acc.UserID, householdID)
}

func (s *Server) callbackURI(r *http.Request) string {
	return s.absolute(r, "/oauth/callback")
}

func (s *Server) prepareOAuthForm(w http.ResponseWriter, r *http.Request, v *View) {
	state, err := token.New()
	if err != nil {
		return
	}
	s.setCookie(w, oauthStateCookie, state, 600, true)
	v.ClientID = oauth.ClientID
	v.RedirectURI = s.callbackURI(r)
	v.OAuthState = state
}

func (s *Server) oauthStateOK(r *http.Request) bool {
	c, err := r.Cookie(oauthStateCookie)
	if err != nil || !token.Valid(c.Value) {
		return false
	}
	return sameToken(c.Value, r.FormValue("state"))
}

func (s *Server) beginGrant(w http.ResponseWriter, r *http.Request, user store.User, invite string) {
	if !s.oauthStateOK(r) {
		http.Error(w, "The form expired. Refresh the page and try again.", http.StatusForbidden)
		return
	}
	redirectURI := s.callbackURI(r)
	raw, err := s.store.CreateAuthCode(r.Context(), oauth.ClientID, user.ID, redirectURI, invite)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	target := redirectURI + "?code=" + url.QueryEscape(raw) + "&state=" + url.QueryEscape(r.FormValue("state"))
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *Server) getAuthorize(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("client_id") != oauth.ClientID || r.URL.Query().Get("redirect_uri") != s.callbackURI(r) || r.URL.Query().Get("response_type") != "code" {
		http.Error(w, "That sign-in request is not valid.", http.StatusBadRequest)
		return
	}
	if _, _, ok := s.optionalUser(w, r); ok {
		s.redirect(w, r, "/")
		return
	}
	v := s.authBase(r, "Log in")
	v.Content = "login"
	s.attachInvite(r, &v)
	s.prepareOAuthForm(w, r, &v)
	s.render(w, http.StatusOK, "auth", v)
}

func (s *Server) postAuthorize(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.allow("login:" + clientIP(r, s.cfg.TrustProxy)) {
		s.authError(w, r, "login", "Too many attempts. Wait a few minutes and try again.", http.StatusTooManyRequests)
		return
	}
	v := s.authBase(r, "Log in")
	v.Content = "login"
	s.attachInvite(r, &v)
	v.Email = r.FormValue("email")
	if !s.oauthStateOK(r) {
		v.Error = "The form expired. Refresh the page and try again."
		s.prepareOAuthForm(w, r, &v)
		s.render(w, http.StatusForbidden, "auth", v)
		return
	}
	email, emailOK := cleanEmail(r.FormValue("email"))
	password := r.FormValue("password")
	user, err := s.store.UserByEmail(r.Context(), email)
	if !emailOK || err != nil {
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			s.fail(w, r, err)
			return
		}
		_ = bcrypt.CompareHashAndPassword(dummyPasswordHash(), []byte(password))
		v.Error = "Email or password is incorrect."
		s.prepareOAuthForm(w, r, &v)
		s.render(w, http.StatusOK, "auth", v)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		v.Error = "Email or password is incorrect."
		s.prepareOAuthForm(w, r, &v)
		s.render(w, http.StatusOK, "auth", v)
		return
	}
	s.beginGrant(w, r, user, v.Token)
}

func (s *Server) oauthCallback(w http.ResponseWriter, r *http.Request) {
	if !s.oauthStateOK(r) || r.URL.Query().Get("state") == "" {
		http.Error(w, "The form expired. Refresh the page and try again.", http.StatusForbidden)
		return
	}
	code := r.URL.Query().Get("code")
	userID, invite, err := s.store.ConsumeAuthCode(r.Context(), code, oauth.ClientID, s.callbackURI(r))
	if err != nil {
		http.Error(w, "That sign-in link is no longer valid.", http.StatusBadRequest)
		return
	}
	user, err := s.store.UserByID(r.Context(), userID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	refresh, err := s.store.CreateRefresh(r.Context(), oauth.ClientID, user.ID, "", refreshTTL)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.setCookie(w, refreshCookie, refresh, cookieSeconds, true)
	if err := s.writeAccessCookie(w, user.ID, ""); err != nil {
		s.fail(w, r, err)
		return
	}
	s.clearCookie(w, oauthStateCookie)
	s.rotateCSRF(w)
	if token.Valid(invite) {
		householdID, err := s.store.AcceptInvite(r.Context(), invite, user.ID, user.Email)
		switch {
		case err == nil:
			acc := store.Access{UserID: user.ID, RefreshRaw: refresh}
			if err := s.activateHousehold(w, r, acc, householdID); err != nil {
				s.fail(w, r, err)
				return
			}
			s.redirect(w, r, "/?notice=joined")
			return
		case errors.Is(err, store.ErrEmailMismatch), errors.Is(err, store.ErrExpired), errors.Is(err, store.ErrUsed), errors.Is(err, store.ErrNotFound):
			s.redirect(w, r, "/invites/"+invite)
			return
		default:
			s.fail(w, r, err)
			return
		}
	}
	s.redirect(w, r, "/")
}

func (s *Server) oauthToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		oauthError(w, "invalid_request", http.StatusBadRequest)
		return
	}
	if !secretOK(r.FormValue("client_secret"), s.clientSecret) || r.FormValue("client_id") != oauth.ClientID {
		oauthError(w, "invalid_client", http.StatusUnauthorized)
		return
	}
	var (
		access, refresh string
		err             error
	)
	switch r.FormValue("grant_type") {
	case "authorization_code":
		userID, _, consumeErr := s.store.ConsumeAuthCode(r.Context(), r.FormValue("code"), oauth.ClientID, r.FormValue("redirect_uri"))
		if consumeErr != nil {
			oauthError(w, "invalid_grant", http.StatusBadRequest)
			return
		}
		refresh, err = s.store.CreateRefresh(r.Context(), oauth.ClientID, userID, "", refreshTTL)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		access, err = s.signAccess(userID, "")
	case "refresh_token":
		userID, householdID, next, rotateErr := s.store.RotateRefresh(r.Context(), r.FormValue("refresh_token"), oauth.ClientID, refreshTTL)
		if rotateErr != nil {
			oauthError(w, "invalid_grant", http.StatusBadRequest)
			return
		}
		refresh = next
		access, err = s.signAccess(userID, householdID)
	default:
		oauthError(w, "unsupported_grant_type", http.StatusBadRequest)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token":  access,
		"token_type":    "Bearer",
		"expires_in":    int(accessTTL.Seconds()),
		"refresh_token": refresh,
	})
}

func (s *Server) oauthRevoke(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	if secretOK(r.FormValue("client_secret"), s.clientSecret) && r.FormValue("client_id") == oauth.ClientID {
		_ = s.store.RevokeRefresh(r.Context(), r.FormValue("token"))
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) jwks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(s.signer.JWKS())
}

func (s *Server) signAccess(userID, householdID string) (string, error) {
	now := time.Now()
	jti, err := token.New()
	if err != nil {
		return "", err
	}
	return s.signer.Sign(oauth.Claims{
		Sub:       userID,
		Iss:       oauth.Issuer,
		Aud:       oauth.ClientID,
		Exp:       now.Add(accessTTL).Unix(),
		Iat:       now.Unix(),
		JTI:       jti,
		Household: householdID,
	})
}

func oauthError(w http.ResponseWriter, code string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

func secretOK(got, want string) bool {
	if len(got) != len(want) || want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
