// Command platform runs the procurement agents on a Lagrange server instead of on local
// ports: the buyer and both suppliers connect through tunnels, find each other in
// Lagrange's directory and call each other through it, so the server's live graph and
// task tree show the whole handoff.
//
//	lagrange serve   # or the docker compose stack
//	go run ./cmd/platform -server http://localhost:8090 -email you@example.com -password ...
//
// With -seed it also sends a scripted set of orders (approved, auto-approved, rejected,
// failed, one left waiting for you, a plain message). A dev server can run it for you:
// LAGRANGE_DEV=1 LAGRANGE_DEV_SEED="path/to/platform" lagrange serve.
//
// It logs in with a Lagrange account, creates the addresses @acme-buyer, @northwind and
// @contoso if they don't exist, and mints a fresh token for each. Then message
// @acme-buyer from the web app's inbox: "Order 200 hoodies, budget 6000 EUR, deliver by
// <date>". The approval question comes back to your inbox.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2aclient"

	"github.com/three-agent-problem/examples/procurement/buyer"
	"github.com/three-agent-problem/examples/procurement/internal/agentkit"
	"github.com/three-agent-problem/examples/procurement/supplier"
	"github.com/three-agent-problem/lagrange/delegate"
	"github.com/three-agent-problem/lagrange/tunnel"
	regclient "github.com/three-agent-problem/registry/client"
)

func main() {
	server := flag.String("server", envOr("LAGRANGE_SERVER", "http://localhost:8090"), "Lagrange server URL.")
	email := flag.String("email", os.Getenv("LAGRANGE_ADMIN_EMAIL"), "Lagrange account email.")
	password := flag.String("password", os.Getenv("LAGRANGE_ADMIN_PASSWORD"), "Lagrange account password.")
	step := flag.Duration("fulfilment-step", 2*time.Second, "Time between supplier progress updates.")
	workers := flag.Int("supplier-workers", 2, "Tunnels per supplier (a worker pool).")
	seeding := flag.Bool("seed", os.Getenv("LAGRANGE_SEED") == "1", "Send a scripted set of orders once the agents are online (env LAGRANGE_SEED=1).")
	flag.Parse()
	*server = strings.TrimSuffix(*server, "/")
	if *email == "" || *password == "" {
		log.Fatal("platform: -email and -password are required")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Retry for a while: as a dev server's seed command, this starts before the server listens.
	var api *apiClient
	var err error
	for try := 0; ; try++ {
		if api, err = login(ctx, *server, *email, *password); err == nil || try == 60 || ctx.Err() != nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if err != nil {
		log.Fatalf("platform: %v", err)
	}
	tokens := map[string]string{}
	for _, a := range []struct{ handle, name, desc string }{
		{"acme-buyer", "Acme Procurement", "Buys merchandise for Acme: compares supplier quotes, gets approvals, places and tracks orders."},
		{"northwind", "Northwind Merch", "Quotes and fulfils branded merchandise orders. Fast."},
		{"contoso", "Contoso Prints", "Quotes and fulfils branded merchandise orders. Cheap but slow."},
	} {
		if tokens[a.handle], err = api.token(ctx, a.handle, a.name, a.desc); err != nil {
			log.Fatalf("platform: %s: %v", a.handle, err)
		}
	}

	quiet := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	run := func(handle string, h http.Handler, expose bool) {
		go func() {
			err := tunnel.Run(ctx, tunnel.ClientConfig{
				Server: *server, Token: tokens[handle], Capacity: 4, Handler: h, Expose: expose,
				UserAgent: "procurement-demo/" + handle, Logger: quiet,
			})
			if err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("tunnel %s: %v", handle, err)
			}
		}()
	}

	// On Lagrange an agent's public URL is its address, <server>/a/<handle>: suppliers put
	// it in their quotes, and the buyer orders from it. Requests that reach the agent
	// through the tunnel carry the same path, so atAddress strips the prefix.
	address := func(handle string) string { return *server + "/a/" + handle }
	for _, s := range []struct {
		handle string
		cfg    supplier.Config
	}{
		{"northwind", supplier.Config{Name: "Northwind Merch", BaseURL: address("northwind"), UnitPrice: 28.50, LeadDays: 7, Step: *step}},
		{"contoso", supplier.Config{Name: "Contoso Prints", BaseURL: address("contoso"), UnitPrice: 24.00, LeadDays: 30, Step: *step}},
	} {
		agent := supplier.New(s.cfg)
		h := atAddress(s.handle, agentkit.Mux(agent.Card(), agent, agentkit.Observe{}))
		for range max(1, *workers) {
			run(s.handle, h, false)
		}
	}

	b := buyer.New(buyer.Config{
		BaseURL:          address("acme-buyer"),
		WebhookURL:       *server + "/t/acme-buyer" + buyer.WebhookPath,
		Registry:         regclient.New(*server+"/registry", nil),
		CallInterceptors: []a2aclient.CallInterceptor{delegate.Interceptor(tokens["acme-buyer"])},
	})
	mux := agentkit.Mux(b.Card(), delegate.Executor(b), agentkit.Observe{})
	mux.Handle(buyer.WebhookPath, b.Webhook())
	run("acme-buyer", atAddress("acme-buyer", mux), true)

	if *seeding {
		go seed(ctx, api, []string{"acme-buyer", "northwind", "contoso"})
	}

	deliverBy := time.Now().AddDate(0, 0, 21).Format("2006-01-02")
	fmt.Printf(`
The procurement agents are connected to %s

  @acme-buyer   buyer, orchestrator (exposes /t/acme-buyer/webhook for supplier pushes)
  @northwind    supplier, fast       (%d workers)
  @contoso      supplier, cheap/slow (%d workers)

Open the web app, go to the Inbox and message @acme-buyer:

  Order 200 hoodies, budget 6000 EUR, deliver by %s

Watch the Live graph, approve the order when it asks, then open the task tree.
Ctrl-C disconnects the agents (their tasks wait in the mailbox until they're back).
`, *server, *workers, *workers, deliverBy)
	<-ctx.Done()
}

// atAddress serves h both at / and under /a/<handle>/.
func atAddress(handle string, h http.Handler) http.Handler {
	prefixed := http.StripPrefix("/a/"+handle, h)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/a/"+handle+"/") {
			prefixed.ServeHTTP(w, r)
			return
		}
		h.ServeHTTP(w, r)
	})
}

type apiClient struct {
	server string
	http   *http.Client
}

func login(ctx context.Context, server, email, password string) (*apiClient, error) {
	jar, _ := cookiejar.New(nil)
	c := &apiClient{server: server, http: &http.Client{Jar: jar, Timeout: 15 * time.Second}}
	if _, err := c.post(ctx, "/api/login", map[string]string{"email": email, "password": password}, http.StatusOK); err != nil {
		return nil, fmt.Errorf("logging in: %w", err)
	}
	return c, nil
}

// token creates the address if needed and returns a new token for it.
func (c *apiClient) token(ctx context.Context, handle, name, description string) (string, error) {
	body, err := c.post(ctx, "/api/addresses", map[string]string{"handle": handle, "displayName": name, "description": description}, http.StatusCreated)
	var status statusError
	if errors.As(err, &status) && status.code == http.StatusConflict {
		body, err = c.post(ctx, "/api/addresses/"+handle+"/tokens", map[string]string{"label": "procurement demo"}, http.StatusCreated)
	}
	if err != nil {
		return "", err
	}
	var out struct{ Token string }
	if err := json.Unmarshal(body, &out); err != nil || out.Token == "" {
		return "", fmt.Errorf("no token in %s", body)
	}
	return out.Token, nil
}

type statusError struct {
	code int
	body string
}

func (e statusError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.code, e.body) }

func (c *apiClient) post(ctx context.Context, path string, v any, want int) ([]byte, error) {
	raw, _ := json.Marshal(v)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.server+path, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	if resp.StatusCode != want {
		return nil, statusError{resp.StatusCode, strings.TrimSpace(buf.String())}
	}
	return buf.Bytes(), nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
