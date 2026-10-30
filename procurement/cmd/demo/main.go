// Command demo runs the procurement handoff demo: a registry, a console, two supplier
// agents and a buyer agent, all on localhost.
//
//	go run ./cmd/demo
//	a2a send http://localhost:9001 "Order 200 hoodies, budget 6000 EUR, deliver by 2026-11-20, add to my calendar"
//	open http://localhost:8088/inbox
//
// With -credentials=nango the calendar step uses Nango Connect and the Nango proxy
// (set NANGO_SECRET_KEY, and create a Google Calendar integration in Nango first).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/three-agent-problem/examples/procurement/stack"
)

func main() {
	cfg := stack.Config{}
	flag.StringVar(&cfg.Host, "host", "localhost", "Host name used in URLs.")
	flag.IntVar(&cfg.RegistryPort, "registry-port", 7070, "Registry port (7000 is taken by macOS AirPlay).")
	flag.IntVar(&cfg.ConsolePort, "console-port", 8088, "Console port (8080 is often taken by other dev servers).")
	flag.IntVar(&cfg.BuyerPort, "buyer-port", 9001, "Buyer agent port.")
	flag.IntVar(&cfg.NorthwindPort, "northwind-port", 9101, "Northwind Merch supplier port.")
	flag.IntVar(&cfg.ContosoPort, "contoso-port", 9102, "Contoso Prints supplier port.")
	flag.StringVar(&cfg.Credentials, "credentials", stack.CredentialsDemo, "Calendar credentials broker: demo, nango or none.")
	flag.StringVar(&cfg.NangoSecretKey, "nango-secret-key", os.Getenv("NANGO_SECRET_KEY"), "Nango secret key (default $NANGO_SECRET_KEY).")
	flag.StringVar(&cfg.NangoBaseURL, "nango-base-url", envOr("NANGO_BASE_URL", "https://api.nango.dev"), "Nango API URL; change it for self-hosted Nango.")
	flag.StringVar(&cfg.NangoWebhookSigningKey, "nango-webhook-signing-key", os.Getenv("NANGO_WEBHOOK_SIGNING_KEY"), "Verifies Nango webhooks when they can reach the console.")
	flag.StringVar(&cfg.Integration, "integration", envOr("NANGO_INTEGRATION", "google-calendar"), "Integration ID of Google Calendar in the broker.")
	flag.StringVar(&cfg.SlackWebhook, "slack-webhook", os.Getenv("SLACK_WEBHOOK_URL"), "Slack incoming webhook told about tasks that wait on a person.")
	flag.DurationVar(&cfg.FulfilmentStep, "fulfilment-step", 2*time.Second, "Time between supplier progress updates.")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	s, err := stack.Start(ctx, cfg)
	if err != nil {
		log.Fatalf("demo: %v", err)
	}
	defer s.Close()

	deliverBy := time.Now().AddDate(0, 0, 21).Format("2006-01-02")
	fmt.Printf(`
Procurement handoff demo is running.

  Registry          %s
  Console / inbox   %s  (%s/inbox)
  Acme Procurement  %s  (buyer, orchestrator)
  Northwind Merch   %s  (supplier, fast)
  Contoso Prints    %s  (supplier, cheap but slow)
  Credentials       %s

Try:
  a2a send %s "Order 200 hoodies, budget 6000 EUR, deliver by %s, add to my calendar"

Then approve the order in the inbox, connect the calendar when asked, and watch the
task tree in the console. Ctrl-C stops everything.
`, s.RegistryURL, s.ConsoleURL, s.ConsoleURL, s.BuyerURL, s.NorthwindURL, s.ContosoURL, cfg.Credentials, s.BuyerURL, deliverBy)

	<-ctx.Done()
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
