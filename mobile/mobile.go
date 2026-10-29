// Package mobile is the in-process entry point for the Android and iOS shells.
// gomobile bind exports Start and Stop.
package mobile

import (
	"path/filepath"
	"strings"
	"sync"

	"hearth/internal/app"
)

var (
	mu      sync.Mutex
	running *app.Running
)

// Start serves Hearth from dataDir and returns the local URL.
// A non-empty remoteURL opens that address instead and does not start a server.
func Start(dataDir, remoteURL string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	if running != nil {
		return running.URL, nil
	}
	var (
		next *app.Running
		err  error
	)
	if strings.TrimSpace(remoteURL) != "" {
		next, err = app.UseRemote(remoteURL)
	} else {
		next, err = app.ServeLocal(filepath.Join(dataDir, "hearth.db"), filepath.Join(dataDir, "hearth.log"))
	}
	if err != nil {
		return "", err
	}
	running = next
	return running.URL, nil
}

// Stop shuts down the in-process server. A remote URL has nothing to stop.
func Stop() {
	mu.Lock()
	defer mu.Unlock()
	if running == nil {
		return
	}
	_ = running.Close()
	running = nil
}
