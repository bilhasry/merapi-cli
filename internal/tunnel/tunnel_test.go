package tunnel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// writeFakeBin drops an executable shell script named name into a temp dir and
// points PATH at it (plus the existing PATH).
func writeFakeBin(t *testing.T, name, script string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake tunnel binaries are shell scripts")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestDetect(t *testing.T) {
	writeFakeBin(t, "cloudflared", "exit 0\n")
	if got := Detect(); len(got) != 1 || got[0] != Cloudflared {
		t.Errorf("Detect() = %v, want [cloudflared]", got)
	}
}

func TestStartRequiresTarget(t *testing.T) {
	if _, err := Start(context.Background(), Options{}); err == nil {
		t.Error("expected error without a target")
	}
}

func TestStartUnsupportedProvider(t *testing.T) {
	_, err := Start(context.Background(), Options{Provider: "frp", Target: "127.0.0.1:9090"})
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("err = %v, want unsupported-provider error", err)
	}
}

func TestStartMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // empty PATH: nothing installed
	_, err := Start(context.Background(), Options{Provider: "ngrok", Target: "127.0.0.1:9090"})
	if err == nil || !strings.Contains(err.Error(), "not found in PATH") {
		t.Errorf("err = %v, want not-found-in-PATH error", err)
	}
}

func TestStartAutoWithoutBinaries(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := Start(context.Background(), Options{Provider: "auto", Target: "127.0.0.1:9090"})
	if err == nil || !strings.Contains(err.Error(), "no tunneling binary") {
		t.Errorf("err = %v, want no-binary error", err)
	}
}

func TestStartCloudflaredParsesOutput(t *testing.T) {
	writeFakeBin(t, "cloudflared", `echo "INF Your quick Tunnel has been created! Visit it at https://mock-123.trycloudflare.com"
sleep 30`)
	tun, err := Start(context.Background(), Options{Provider: Cloudflared, Target: "127.0.0.1:9090", Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer tun.Stop()
	if tun.PublicURL != "https://mock-123.trycloudflare.com" {
		t.Errorf("PublicURL = %q", tun.PublicURL)
	}
	if tun.Provider != Cloudflared {
		t.Errorf("Provider = %q", tun.Provider)
	}
}

func TestStartNgrokUsesLocalAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tunnels": []map[string]any{
				{"public_url": "http://abc.ngrok.io", "proto": "http"},
				{"public_url": "https://abc.ngrok-free.app", "proto": "https"},
			},
		})
	}))
	defer srv.Close()
	old := ngrokAPIBase
	ngrokAPIBase = srv.URL
	defer func() { ngrokAPIBase = old }()

	writeFakeBin(t, "ngrok", "sleep 30")
	tun, err := Start(context.Background(), Options{Provider: Ngrok, Target: "127.0.0.1:9090", Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer tun.Stop()
	if tun.PublicURL != "https://abc.ngrok-free.app" {
		t.Errorf("PublicURL = %q, want the https tunnel", tun.PublicURL)
	}
}

func TestStartNgrokFallsBackToOutput(t *testing.T) {
	// Point the local API at a dead address so the URL must come from output
	// regardless of what else runs on this machine.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	dead := srv.URL
	srv.Close()
	old := ngrokAPIBase
	ngrokAPIBase = dead
	defer func() { ngrokAPIBase = old }()

	writeFakeBin(t, "ngrok", "echo 'url=https://def.ngrok-free.app'\nsleep 30")
	tun, err := Start(context.Background(), Options{Provider: Ngrok, Target: "127.0.0.1:9090", Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer tun.Stop()
	if tun.PublicURL != "https://def.ngrok-free.app" {
		t.Errorf("PublicURL = %q", tun.PublicURL)
	}
}

func TestStartReportsEarlyExit(t *testing.T) {
	writeFakeBin(t, "cloudflared", `echo "ERR failed to connect"; exit 1`)
	_, err := Start(context.Background(), Options{Provider: Cloudflared, Target: "127.0.0.1:9090", Timeout: 10 * time.Second})
	if err == nil || !strings.Contains(err.Error(), "exited before publishing") {
		t.Fatalf("err = %v, want early-exit error", err)
	}
	if !strings.Contains(err.Error(), "failed to connect") {
		t.Errorf("error should include provider output: %v", err)
	}
}

func TestStopIsSafeWhenNotStarted(t *testing.T) {
	var nilTun *Tunnel
	nilTun.Stop() // must not panic
	(&Tunnel{}).Stop()
}

func TestTail(t *testing.T) {
	if got := tail("short\n"); got != "short" {
		t.Errorf("tail(short) = %q", got)
	}
	long := strings.Repeat("x", 1000)
	if got := tail(long); len(got) != 503 || !strings.HasPrefix(got, "…") {
		t.Errorf("tail(long) length = %d, want 503 (3-byte ellipsis + 500 chars)", len(got))
	}
}
