package cli

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bilhasry/merapi/internal/gql"
	"github.com/bilhasry/merapi/internal/mock"
	"github.com/bilhasry/merapi/internal/tunnel"
	"github.com/spf13/cobra"
)

func durationMs(ms int) time.Duration {
	return time.Duration(ms) * time.Millisecond
}

func newMockCmd() *cobra.Command {
	var addr string
	var delayMs int
	var tunnelProvider string
	cmd := &cobra.Command{
		Use:   "mock",
		Short: "Serve deterministic mock responses for every documented endpoint",
		Long: `Serve deterministic mock responses for every endpoint in the OpenAPI document.

Each operation answers with its documented success response (lowest 2xx status).
The body is the response example when set, else generated from the response
schema. Write operations (POST/PUT/PATCH) echo the request body over the
generated shape, and path parameters fill matching object properties.

Send "X-Mock-Status: <code>" to receive another documented response (e.g.
X-Mock-Status: 404) — useful for exercising error handling.

GET / lists every mocked operation.

With --tunnel, the mock is also exposed to the internet through ngrok or
cloudflared (the binary must be installed and in PATH) — useful for testing
webhooks and sharing a temporary API with remote clients. The public HTTPS URL
is printed once the tunnel is up.`,
		Example: `  merapi mock                          # http://127.0.0.1:9090
  merapi mock --addr 0.0.0.0:8080
  merapi mock --delay 250             # simulate latency (ms)
  merapi mock --tunnel auto           # expose via ngrok or cloudflared
  merapi mock --tunnel ngrok          # force a provider`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, d, err := mustDoc()
			if err != nil {
				return err
			}
			// Load the GraphQL SDL if present — adds POST /graphql to the mock.
			var gqlSchema *gql.Schema
			if sdlPath := p.GraphQLPath(); sdlPath != "" {
				gqlSchema, err = gql.Load(sdlPath)
				if err != nil {
					return fmt.Errorf("GraphQL SDL: %w", err)
				}
			}
			srv := mock.New(d, mock.Options{
				Log:     os.Stdout,
				Delay:   durationMs(delayMs),
				GraphQL: gqlSchema,
			})
			routes := srv.Routes()
			if len(routes) == 0 {
				return fmt.Errorf("no endpoints documented — add one with `merapi endpoint add`")
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			if !flagJSON && !flagQuiet {
				fmt.Printf("Mocking %d operation(s) for %q:\n\n", len(routes), d.Title())
				table([]string{"METHOD", "PATH", "STATUS", "OPERATION"}, rows(routes))
				if gqlSchema != nil {
					fmt.Printf("\nGraphQL: POST /graphql (served from %s)\n", p.GraphQLPath())
				}
				fmt.Printf("\nListening on http://%s — GET / for the index, Ctrl+C to stop\n", addr)
				if tunnelProvider != "" {
					fmt.Printf("Tunnel: starting %s…\n", tunnelProvider)
				}
				fmt.Println()
			}

			// Serve in the background, then bring the tunnel up once the
			// mock is actually accepting connections.
			errCh := make(chan error, 1)
			go func() { errCh <- srv.Run(ctx, addr) }()

			if tunnelProvider != "" {
				if err := waitForServer(ctx, addr); err != nil {
					stop()
					return err
				}
				tun, terr := tunnel.Start(ctx, tunnel.Options{
					Provider: tunnel.Provider(tunnelProvider),
					Target:   addr,
				})
				if terr != nil {
					// Keep serving locally — a missing/failing tunnel binary
					// should not take the mock down.
					if !flagJSON && !flagQuiet {
						fmt.Printf("⚠ tunnel failed (%v) — mock still available at http://%s\n\n", terr, addr)
					}
				} else {
					defer tun.Stop()
					if !flagJSON && !flagQuiet {
						fmt.Printf("Public %s URL: %s\n\n", tun.Provider, tun.PublicURL)
						fmt.Println("(anyone with this URL can reach the mock — Ctrl+C to stop)")
					}
				}
			}

			if err := <-errCh; err != nil {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:9090", "address to listen on")
	cmd.Flags().IntVar(&delayMs, "delay", 0, "artificial latency per response, in milliseconds")
	cmd.Flags().StringVar(&tunnelProvider, "tunnel", "", `expose the mock publicly through a tunnel: auto (pick ngrok or cloudflared), ngrok, or cloudflared — requires the binary in PATH`)
	return cmd
}

// waitForServer blocks until addr accepts TCP connections (or ctx ends).
func waitForServer(ctx context.Context, addr string) error {
	deadline := time.Now().Add(10 * time.Second)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		conn, err := net.DialTimeout("tcp", addr, 250*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("mock server did not start listening on %s: %v", addr, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func rows(routes []mock.Route) [][]string {
	out := make([][]string, 0, len(routes))
	for _, r := range routes {
		out = append(out, []string{r.Method, r.Path, fmt.Sprintf("%d", r.Status), r.OperationID})
	}
	return out
}
