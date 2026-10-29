package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"hearth/internal/config"
	"hearth/internal/store"
	"hearth/internal/web"
)

// ErrAlreadyRunning means another Hearth process holds the data directory lock.
var ErrAlreadyRunning = errors.New("hearth is already running")

// Running is either an in-process server or a remote origin the shell should open.
type Running struct {
	URL    string
	server *http.Server
	store  *store.Store
	log    *os.File
}

// DesktopPaths is the SQLite file, the log file, and the directory that holds both.
// HEARTH_DATA overrides the database path. Otherwise the files live under the user config directory.
func DesktopPaths() (db, logFile, dir string, err error) {
	if v := strings.TrimSpace(os.Getenv("HEARTH_DATA")); v != "" {
		dir = filepath.Dir(v)
		return v, filepath.Join(dir, "hearth.log"), dir, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", "", "", err
	}
	dir = filepath.Join(base, "hearth")
	return filepath.Join(dir, "hearth.db"), filepath.Join(dir, "hearth.log"), dir, nil
}

// UseRemote checks a future central-server URL. No database is opened.
func UseRemote(raw string) (*Running, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("HEARTH_URL must be an http or https URL")
	}
	return &Running{URL: u.String()}, nil
}

// ServeLocal listens on 127.0.0.1 with an ephemeral port and serves Hearth from dataFile.
func ServeLocal(dataFile, logFile string) (*Running, error) {
	if err := os.MkdirAll(filepath.Dir(dataFile), 0o755); err != nil {
		return nil, err
	}
	lf, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	log := slog.New(slog.NewJSONHandler(lf, nil))
	st, err := store.Open(dataFile)
	if err != nil {
		lf.Close()
		return nil, err
	}
	env := getenv("HEARTH_ENV", "development")
	cfg := config.Config{
		DataPath:      dataFile,
		Env:           env,
		SecureCookies: secureCookies(env),
		BaseURL:       strings.TrimRight(strings.TrimSpace(os.Getenv("HEARTH_BASE_URL")), "/"),
		TrustProxy:    os.Getenv("HEARTH_TRUST_PROXY") == "true" || os.Getenv("HEARTH_TRUST_PROXY") == "1",
	}
	srv, err := web.New(cfg, st, log)
	if err != nil {
		st.Close()
		lf.Close()
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		st.Close()
		lf.Close()
		return nil, err
	}
	httpServer := &http.Server{
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		log.Info("listening", "addr", ln.Addr().String())
		if err := httpServer.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("listen", "err", err)
		}
	}()
	return &Running{
		URL:    "http://" + ln.Addr().String(),
		server: httpServer,
		store:  st,
		log:    lf,
	}, nil
}

// Close shuts the in-process server down. A remote origin has nothing to stop.
func (r *Running) Close() error {
	if r == nil {
		return nil
	}
	var err error
	if r.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err = r.server.Shutdown(ctx)
	}
	if r.store != nil {
		r.store.Close()
	}
	if r.log != nil {
		r.log.Close()
	}
	return err
}

func getenv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func secureCookies(env string) bool {
	if v, ok := os.LookupEnv("HEARTH_SECURE_COOKIES"); ok {
		return v == "true" || v == "1"
	}
	return env == "production"
}
