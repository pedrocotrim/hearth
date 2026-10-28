package web

import (
	"context"
	"crypto/subtle"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"hearth/internal/token"
)

func (s *Server) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("panic", "err", rec, "stack", string(debug.Stack()))
				http.Error(w, "Something went wrong.", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || strings.HasPrefix(r.URL.Path, "/static/") {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		s.log.Log(r.Context(), slog.LevelInfo, "request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"duration", time.Since(start).String(),
		)
	})
}

func (s *Server) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; form-action 'self'; base-uri 'self'; frame-ancestors 'none'")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("X-Robots-Tag", "noindex")
		if s.cfg.SecureCookies {
			w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		csrf := ""
		if c, err := r.Cookie(csrfCookie); err == nil && token.Valid(c.Value) {
			csrf = c.Value
		}
		if csrf == "" {
			fresh, err := token.New()
			if err != nil {
				http.Error(w, "Something went wrong.", http.StatusInternalServerError)
				return
			}
			csrf = fresh
			s.setCookie(w, csrfCookie, csrf, cookieSeconds, true)
		}
		if !safeMethod(r.Method) && !csrfExempt(r) {
			sent := r.FormValue("_csrf")
			if sent == "" {
				sent = r.Header.Get("X-CSRF-Token")
			}
			if !sameToken(sent, csrf) {
				http.Error(w, "The form expired. Refresh the page and try again.", http.StatusForbidden)
				return
			}
		}
		ctx := context.WithValue(r.Context(), ctxCSRF, csrf)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) setCookie(w http.ResponseWriter, name, value string, maxAge int, httpOnly bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: httpOnly,
		Secure:   s.cfg.SecureCookies,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearCookie(w http.ResponseWriter, name string) {
	s.setCookie(w, name, "", -1, true)
}

func (s *Server) rotateCSRF(w http.ResponseWriter) {
	fresh, err := token.New()
	if err != nil {
		return
	}
	s.setCookie(w, csrfCookie, fresh, cookieSeconds, true)
}

func csrfExempt(r *http.Request) bool {
	return r.URL.Path == "/oauth/token" || r.URL.Path == "/oauth/revoke"
}

func safeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}

func sameToken(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func clientIP(r *http.Request, trust bool) string {
	if trust {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			part := strings.TrimSpace(strings.Split(xff, ",")[0])
			if part != "" {
				return part
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type rateLimiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	limit  int
	window time.Duration
}

func newLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{
		hits:   map[string][]time.Time{},
		limit:  limit,
		window: window,
	}
}

func (l *rateLimiter) allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.hits) > 10000 {
		l.hits = map[string][]time.Time{}
	}
	cut := now.Add(-l.window)
	prev := l.hits[key]
	kept := prev[:0]
	for _, ts := range prev {
		if ts.After(cut) {
			kept = append(kept, ts)
		}
	}
	if len(kept) >= l.limit {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	return true
}

type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusWriter) WriteHeader(code int) {
	if w.wrote {
		return
	}
	w.wrote = true
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
