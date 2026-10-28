package web

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"path/filepath"
	"time"

	"hearth/internal/config"
	"hearth/internal/oauth"
	"hearth/internal/store"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

const (
	accessCookie     = "hearth_access"
	refreshCookie    = "hearth_refresh"
	oauthStateCookie = "hearth_oauth_state"
	csrfCookie       = "hearth_csrf"
	inviteCookie     = "hearth_invite"
	accessTTL        = 15 * time.Minute
	refreshTTL       = 14 * 24 * time.Hour
	inviteTTL        = 14 * 24 * time.Hour
	cookieSeconds    = 14 * 24 * 60 * 60
	bcryptCost       = 12
)

type ctxKey int

const (
	ctxUser ctxKey = iota
	ctxAccess
	ctxCSRF
)

var errNoHousehold = errors.New("no household")

type Server struct {
	cfg          config.Config
	store        *store.Store
	log          *slog.Logger
	tmpl         *template.Template
	static       http.Handler
	limiter      *rateLimiter
	signer       *oauth.Signer
	clientSecret string
}

func New(cfg config.Config, st *store.Store, log *slog.Logger) (*Server, error) {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	tmpl, err := template.ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, err
	}
	limit := 10
	if cfg.Env == "test" {
		limit = 1000
	}
	keyDir := filepath.Dir(cfg.DataPath)
	if keyDir == "" || keyDir == "." {
		keyDir = "data"
	}
	signer, err := oauth.LoadSigner(keyDir)
	if err != nil {
		return nil, err
	}
	secret, err := oauth.LoadClientSecret(keyDir)
	if err != nil {
		return nil, err
	}
	return &Server{
		cfg:          cfg,
		store:        st,
		log:          log,
		tmpl:         tmpl,
		static:       http.StripPrefix("/static/", cacheStatic{http.FileServer(http.FS(sub))}),
		limiter:      newLimiter(limit, 10*time.Minute),
		signer:       signer,
		clientSecret: secret,
	}, nil
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /static/", s.static)
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /robots.txt", s.robots)
	mux.HandleFunc("GET /login", s.getLogin)
	mux.HandleFunc("GET /register", s.getRegister)
	mux.HandleFunc("POST /register", s.postRegister)
	mux.HandleFunc("POST /logout", s.auth(s.postLogout))
	mux.HandleFunc("GET /oauth/authorize", s.getAuthorize)
	mux.HandleFunc("POST /oauth/authorize", s.postAuthorize)
	mux.HandleFunc("GET /oauth/callback", s.oauthCallback)
	mux.HandleFunc("POST /oauth/token", s.oauthToken)
	mux.HandleFunc("POST /oauth/revoke", s.oauthRevoke)
	mux.HandleFunc("GET /.well-known/jwks.json", s.jwks)
	mux.HandleFunc("GET /invites/{token}", s.getInvite)
	mux.HandleFunc("POST /invites/{token}/accept", s.auth(s.postAcceptInvite))
	mux.HandleFunc("POST /invites/{inviteID}/revoke", s.auth(s.postRevokeInvite))

	mux.HandleFunc("GET /{$}", s.auth(s.dashboard))
	mux.HandleFunc("POST /households", s.auth(s.postHousehold))
	mux.HandleFunc("POST /households/switch", s.auth(s.postSwitchHousehold))
	mux.HandleFunc("POST /households/settings", s.auth(s.postHouseholdSettings))

	mux.HandleFunc("GET /members", s.auth(s.members))
	mux.HandleFunc("POST /members/invite", s.auth(s.postInvite))
	mux.HandleFunc("POST /members/participants", s.auth(s.postParticipant))
	mux.HandleFunc("POST /members/{participantID}/remove", s.auth(s.postRemoveMember))

	mux.HandleFunc("GET /utilities", s.auth(s.utilities))
	mux.HandleFunc("POST /utilities", s.auth(s.postUtility))
	mux.HandleFunc("POST /utilities/{utilityID}/exemptions", s.auth(s.postExemptions))

	mux.HandleFunc("GET /bills", s.auth(s.bills))
	mux.HandleFunc("GET /bills/new", s.auth(s.newBill))
	mux.HandleFunc("GET /bills/split-fields", s.auth(s.splitFields))
	mux.HandleFunc("POST /bills", s.auth(s.postBill))
	mux.HandleFunc("GET /bills/{billID}", s.auth(s.showBill))
	mux.HandleFunc("POST /bills/{billID}/payments", s.auth(s.postPayment))
	mux.HandleFunc("POST /bills/{billID}/pay-provider", s.auth(s.postProviderPaid))
	mux.HandleFunc("POST /bills/{billID}/delete", s.auth(s.postDeleteBill))

	var handler http.Handler = mux
	handler = s.csrf(handler)
	handler = s.secure(handler)
	handler = s.logRequests(handler)
	handler = s.recover(handler)
	return handler
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, "ok\n")
}

func (s *Server) robots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, "User-agent: *\nDisallow: /\n")
}

func (s *Server) render(w http.ResponseWriter, status int, layout string, v View) {
	if v.Fields == nil {
		v.Fields = map[string]string{}
	}
	if v.Year == 0 {
		v.Year = time.Now().Year()
	}
	if v.Content != "" {
		var content bytes.Buffer
		if err := s.tmpl.ExecuteTemplate(&content, v.Content, v); err != nil {
			s.log.Error("render", "err", err, "layout", layout, "content", v.Content)
			http.Error(w, "Something went wrong.", http.StatusInternalServerError)
			return
		}
		v.Body = template.HTML(content.String())
	}
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, layout, v); err != nil {
		s.log.Error("render", "err", err, "layout", layout, "content", v.Content)
		http.Error(w, "Something went wrong.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

func (s *Server) renderPartial(w http.ResponseWriter, name string, v View) {
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, name, v); err != nil {
		s.log.Error("render", "err", err, "content", name)
		http.Error(w, "Something went wrong.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(buf.Bytes())
}

func (s *Server) redirect(w http.ResponseWriter, r *http.Request, path string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", path)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, path, http.StatusSeeOther)
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	http.Error(w, "Something went wrong.", http.StatusInternalServerError)
}

func (s *Server) shell(r *http.Request, sc scope, title, active string) View {
	v := View{
		Title:         title,
		Active:        active,
		CSRF:          csrfFrom(r.Context()),
		Notice:        noticeText(r.URL.Query().Get("notice")),
		Year:          time.Now().Year(),
		UserName:      sc.User.Name,
		UserEmail:     sc.User.Email,
		Fields:        map[string]string{},
		DraftCurrency: "USD",
		Currencies:    nil,
	}
	if sc.Household.ID != "" {
		v.HasHousehold = true
		v.HouseholdName = sc.Household.Name
		v.HouseholdID = sc.Household.ID
		v.Currency = sc.Household.Currency
		v.IsOwner = sc.owner()
		for _, h := range sc.Households {
			v.Households = append(v.Households, HouseholdOption{
				ID:       h.ID,
				Name:     h.Name,
				Selected: h.ID == sc.Household.ID,
			})
		}
	}
	return v
}

func (s *Server) scope(w http.ResponseWriter, r *http.Request) (scope, error) {
	return s.scopeFor(w, r, currentUser(r.Context()), currentAccess(r.Context()))
}

func (s *Server) scopeFor(w http.ResponseWriter, r *http.Request, user store.User, acc store.Access) (scope, error) {
	households, err := s.store.ListHouseholds(r.Context(), user.ID)
	if err != nil {
		return scope{}, err
	}
	if len(households) == 0 {
		return scope{User: user, Access: acc}, errNoHousehold
	}
	var selected store.Household
	found := false
	if acc.HouseholdID != "" {
		for _, h := range households {
			if h.ID == acc.HouseholdID {
				selected = h
				found = true
				break
			}
		}
	}
	if !found {
		selected = households[0]
		if err := s.activateHousehold(w, r, acc, selected.ID); err != nil {
			return scope{}, err
		}
		acc.HouseholdID = selected.ID
	}
	membership, err := s.store.Membership(r.Context(), selected.ID, user.ID)
	if err != nil {
		return scope{}, err
	}
	selfID, err := s.store.ParticipantForUser(r.Context(), selected.ID, user.ID)
	if err != nil {
		return scope{}, err
	}
	return scope{
		User:       user,
		Access:     acc,
		Household:  selected,
		Membership: membership,
		Households: households,
		SelfID:     selfID,
	}, nil
}

func (s *Server) mustScope(w http.ResponseWriter, r *http.Request) (scope, bool) {
	sc, err := s.scope(w, r)
	if errors.Is(err, errNoHousehold) {
		s.redirect(w, r, "/")
		return scope{}, false
	}
	if err != nil {
		s.fail(w, r, err)
		return scope{}, false
	}
	return sc, true
}

func (s *Server) optionalUser(w http.ResponseWriter, r *http.Request) (store.Access, store.User, bool) {
	refreshRaw := ""
	if c, err := r.Cookie(refreshCookie); err == nil {
		refreshRaw = c.Value
	}
	if c, err := r.Cookie(accessCookie); err == nil && c.Value != "" {
		if claims, err := s.signer.Verify(c.Value, time.Now()); err == nil {
			user, err := s.store.UserByID(r.Context(), claims.Sub)
			if err == nil {
				return store.Access{UserID: user.ID, HouseholdID: claims.Household, RefreshRaw: refreshRaw}, user, true
			}
		}
	}
	if refreshRaw == "" {
		return store.Access{}, store.User{}, false
	}
	userID, householdID, next, err := s.store.RotateRefresh(r.Context(), refreshRaw, oauth.ClientID, refreshTTL)
	if err != nil {
		s.clearCookie(w, accessCookie)
		s.clearCookie(w, refreshCookie)
		return store.Access{}, store.User{}, false
	}
	user, err := s.store.UserByID(r.Context(), userID)
	if err != nil {
		return store.Access{}, store.User{}, false
	}
	s.setCookie(w, refreshCookie, next, cookieSeconds, true)
	if err := s.writeAccessCookie(w, user.ID, householdID); err != nil {
		return store.Access{}, store.User{}, false
	}
	return store.Access{UserID: user.ID, HouseholdID: householdID, RefreshRaw: next}, user, true
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		acc, user, ok := s.optionalUser(w, r)
		if !ok {
			if r.Header.Get("HX-Request") == "true" {
				w.Header().Set("HX-Redirect", "/login")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		ctx := context.WithValue(r.Context(), ctxUser, user)
		ctx = context.WithValue(ctx, ctxAccess, acc)
		next(w, r.WithContext(ctx))
	}
}

func currentUser(ctx context.Context) store.User {
	user, _ := ctx.Value(ctxUser).(store.User)
	return user
}

func currentAccess(ctx context.Context) store.Access {
	acc, _ := ctx.Value(ctxAccess).(store.Access)
	return acc
}

func csrfFrom(ctx context.Context) string {
	token, _ := ctx.Value(ctxCSRF).(string)
	return token
}

func (s *Server) absolute(r *http.Request, path string) string {
	if s.cfg.BaseURL != "" {
		return s.cfg.BaseURL + path
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	} else if s.cfg.TrustProxy && r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host + path
}

type cacheStatic struct{ h http.Handler }

func (c cacheStatic) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=3600")
	c.h.ServeHTTP(w, r)
}
