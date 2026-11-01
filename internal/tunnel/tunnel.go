// Package tunnel exposes a locally listening service (typically the merapi
// mock server) to the internet through an external tunneling binary such as
// ngrok or cloudflared. merapi does not implement tunneling itself — it
// launches the provider binary, waits for the public URL, and kills the
// tunnel when the parent stops.
package tunnel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Provider identifies a tunneling backend.
type Provider string

// Supported providers (the binary must be installed and in PATH).
const (
	Ngrok       Provider = "ngrok"
	Cloudflared Provider = "cloudflared"
)

// ngrokAPIBase is where ngrok's local inspection API listens by default;
// overridable in tests.
var ngrokAPIBase = "http://127.0.0.1:4040"

// Options configures Start.
type Options struct {
	// Provider selects the backend; empty or "auto" picks the first
	// available of ngrok, cloudflared.
	Provider Provider
	// Target is the local address to expose, e.g. "127.0.0.1:9090".
	Target string
	// Log receives the provider's output; nil discards it.
	Log io.Writer
	// Timeout bounds the wait for the public URL (default 20s).
	Timeout time.Duration
}

// Tunnel is a running tunnel process.
type Tunnel struct {
	Provider  Provider
	PublicURL string
	cmd       *exec.Cmd
}

// Detect lists the tunneling binaries available in PATH.
func Detect() []Provider {
	var out []Provider
	for _, p := range []Provider{Ngrok, Cloudflared} {
		if _, err := exec.LookPath(string(p)); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// Start launches the tunnel and waits until the public URL is known.
func Start(ctx context.Context, opts Options) (*Tunnel, error) {
	if opts.Target == "" {
		return nil, errors.New("tunnel target address is required")
	}
	provider := opts.Provider
	if provider == "" || provider == "auto" {
		avail := Detect()
		if len(avail) == 0 {
			return nil, fmt.Errorf("no tunneling binary found in PATH — install %s or %s", Ngrok, Cloudflared)
		}
		provider = avail[0]
	}
	if provider != Ngrok && provider != Cloudflared {
		return nil, fmt.Errorf("unsupported tunnel provider %q (supported: %s, %s)", provider, Ngrok, Cloudflared)
	}
	if _, err := exec.LookPath(string(provider)); err != nil {
		return nil, fmt.Errorf("%s binary not found in PATH — install it first", provider)
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}

	switch provider {
	case Ngrok:
		return startNgrok(ctx, opts, timeout)
	case Cloudflared:
		return startCloudflared(ctx, opts, timeout)
	default:
		return nil, fmt.Errorf("unsupported tunnel provider %q (supported: %s, %s)", provider, Ngrok, Cloudflared)
	}
}

// Stop kills the tunnel process (safe on a nil or already-stopped tunnel).
func (t *Tunnel) Stop() {
	if t == nil || t.cmd == nil || t.cmd.Process == nil {
		return
	}
	_ = t.cmd.Process.Kill()
}

var (
	ngrokURLRe       = regexp.MustCompile(`https://[a-z0-9.-]+\.ngrok[a-z0-9.-]*`)
	cloudflaredURLRe = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)
)

// startNgrok runs "ngrok http" and resolves the public URL from ngrok's local
// API (preferred) or its output.
func startNgrok(ctx context.Context, opts Options, timeout time.Duration) (*Tunnel, error) {
	buf := &syncBuffer{tee: opts.Log}
	cmd := exec.Command("ngrok", "http", "http://"+opts.Target, "--log=stdout")
	cmd.Stdout, cmd.Stderr = buf, buf
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start ngrok: %w", err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t := &Tunnel{Provider: Ngrok, cmd: cmd}

	url, err := waitURL(ctx, timeout, buf, done, ngrokPublicURL, ngrokURLRe)
	if err != nil {
		t.Stop()
		return nil, err
	}
	t.PublicURL = url
	return t, nil
}

// startCloudflared runs "cloudflared tunnel --url" and parses the ephemeral
// trycloudflare URL from its output.
func startCloudflared(ctx context.Context, opts Options, timeout time.Duration) (*Tunnel, error) {
	buf := &syncBuffer{tee: opts.Log}
	cmd := exec.Command("cloudflared", "tunnel", "--url", "http://"+opts.Target)
	cmd.Stdout, cmd.Stderr = buf, buf
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start cloudflared: %w", err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t := &Tunnel{Provider: Cloudflared, cmd: cmd}

	url, err := waitURL(ctx, timeout, buf, done, nil, cloudflaredURLRe)
	if err != nil {
		t.Stop()
		return nil, err
	}
	t.PublicURL = url
	return t, nil
}

// waitURL polls the provider's API (when it has one) and the captured output
// until a public URL appears, the process dies, or the timeout expires.
func waitURL(ctx context.Context, timeout time.Duration, buf *syncBuffer, done <-chan struct{},
	api func() string, re *regexp.Regexp) (string, error) {

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		if api != nil {
			if u := api(); u != "" {
				return u, nil
			}
		}
		if u := re.FindString(buf.String()); u != "" {
			return u, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-done:
			return "", fmt.Errorf("tunnel process exited before publishing a URL; output:\n%s", tail(buf.String()))
		case <-deadline.C:
			return "", fmt.Errorf("timed out waiting for the public URL; output:\n%s", tail(buf.String()))
		case <-tick.C:
		}
	}
}

// ngrokPublicURL fetches the public https URL from ngrok's local API.
func ngrokPublicURL() string {
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get(ngrokAPIBase + "/api/tunnels")
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	var payload struct {
		Tunnels []struct {
			PublicURL string `json:"public_url"`
			Proto     string `json:"proto"`
		} `json:"tunnels"`
	}
	if json.NewDecoder(resp.Body).Decode(&payload) != nil {
		return ""
	}
	for _, t := range payload.Tunnels {
		if t.Proto == "https" && strings.HasPrefix(t.PublicURL, "https://") {
			return t.PublicURL
		}
	}
	for _, t := range payload.Tunnels {
		if t.PublicURL != "" {
			return t.PublicURL
		}
	}
	return ""
}

// syncBuffer captures provider output (thread-safe) and mirrors it to an
// optional tee writer.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
	tee io.Writer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.tee != nil {
		_, _ = b.tee.Write(p)
	}
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// tail returns the last chunk of s for error messages.
func tail(s string) string {
	const max = 500
	if len(s) <= max {
		return strings.TrimRight(s, "\n")
	}
	return "…" + strings.TrimRight(s[len(s)-max:], "\n")
}
