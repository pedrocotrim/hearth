package app

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestUseRemoteRejectsAPath(t *testing.T) {
	if _, err := UseRemote("hearth.local"); err == nil {
		t.Fatal("expected an error")
	}
	running, err := UseRemote("https://hearth.example/ledger")
	if err != nil {
		t.Fatal(err)
	}
	if running.URL != "https://hearth.example/ledger" {
		t.Fatalf("url %q", running.URL)
	}
	if err := running.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestServeLocalResponds(t *testing.T) {
	dir := t.TempDir()
	running, err := ServeLocal(filepath.Join(dir, "hearth.db"), filepath.Join(dir, "hearth.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = running.Close() })
	resp, err := http.Get(running.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "ok\n" {
		t.Fatalf("health %d %q", resp.StatusCode, body)
	}
}

func TestLockRejectsASecondHolder(t *testing.T) {
	dir := t.TempDir()
	release, err := Lock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(dir); err != ErrAlreadyRunning {
		t.Fatalf("second lock: %v", err)
	}
	release()
	release, err = Lock(dir)
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestDesktopPathsHonorDataEnv(t *testing.T) {
	t.Setenv("HEARTH_DATA", filepath.Join("custom", "house.db"))
	db, logFile, dir, err := DesktopPaths()
	if err != nil {
		t.Fatal(err)
	}
	if db != filepath.Join("custom", "house.db") || dir != "custom" || logFile != filepath.Join("custom", "hearth.log") {
		t.Fatalf("db %q log %q dir %q", db, logFile, dir)
	}
	t.Setenv("HEARTH_DATA", "")
	_, _, dir, err = DesktopPaths()
	if err != nil {
		t.Fatal(err)
	}
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(base, "hearth") {
		t.Fatalf("dir %q", dir)
	}
}
