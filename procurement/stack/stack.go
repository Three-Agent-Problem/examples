// Package stack starts the whole procurement demo in one process: the registry, the
// console, two suppliers and the buyer, each on its own port. cmd/demo and the
// end-to-end test both use it.
package stack

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/three-agent-problem/examples/procurement/buyer"
	"github.com/three-agent-problem/examples/procurement/internal/agentkit"
	"github.com/three-agent-problem/examples/procurement/supplier"
	"github.com/three-agent-problem/lagrange/console"
	"github.com/three-agent-problem/lagrange/credentials"
	"github.com/three-agent-problem/lagrange/credentials/demo"
	"github.com/three-agent-problem/lagrange/credentials/nango"
	regclient "github.com/three-agent-problem/registry/client"
	regserver "github.com/three-agent-problem/registry/server"
)

// Credentials modes.
const (
	CredentialsDemo  = "demo"
	CredentialsNango = "nango"
	CredentialsNone  = "none"
)

// Config configures the stack. Zero ports pick free ports.
type Config struct {
	Host                                                 string
	RegistryPort, ConsolePort, BuyerPort                 int
	NorthwindPort, ContosoPort                           int
	Credentials                                          string
	NangoSecretKey, NangoBaseURL, NangoWebhookSigningKey string
	Integration                                          string
	SlackWebhook                                         string
	// FulfilmentStep is the time between supplier progress updates.
	FulfilmentStep time.Duration
	Logger         *slog.Logger
}

// Stack is a running demo.
type Stack struct {
	RegistryURL, ConsoleURL, BuyerURL, NorthwindURL, ContosoURL string

	Console *console.Console
	// DemoBroker is set when Credentials is CredentialsDemo.
	DemoBroker *demo.Broker

	servers []*http.Server
}

// Start starts every component and registers the agents with the registry.
func Start(ctx context.Context, cfg Config) (*Stack, error) {
	if cfg.Host == "" {
		cfg.Host = "localhost"
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Credentials == "" {
		cfg.Credentials = CredentialsDemo
	}
	s := &Stack{}
	listen := func(port int) (net.Listener, string, error) {
		l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err != nil {
			return nil, "", err
		}
		return l, fmt.Sprintf("http://%s:%d", cfg.Host, l.Addr().(*net.TCPAddr).Port), nil
	}
	var listeners []net.Listener
	open := func(port int, url *string) (net.Listener, error) {
		l, u, err := listen(port)
		if err != nil {
			for _, prev := range listeners {
				_ = prev.Close()
			}
			return nil, err
		}
		listeners = append(listeners, l)
		*url = u
		return l, nil
	}
	regL, err := open(cfg.RegistryPort, &s.RegistryURL)
	if err != nil {
		return nil, err
	}
	conL, err := open(cfg.ConsolePort, &s.ConsoleURL)
	if err != nil {
		return nil, err
	}
	buyL, err := open(cfg.BuyerPort, &s.BuyerURL)
	if err != nil {
		return nil, err
	}
	nwL, err := open(cfg.NorthwindPort, &s.NorthwindURL)
	if err != nil {
		return nil, err
	}
	coL, err := open(cfg.ContosoPort, &s.ContosoURL)
	if err != nil {
		return nil, err
	}

	// Registry.
	reg := regserver.New(nil)
	go reg.Run(ctx, 15*time.Second)
	s.serve(regL, reg.Handler())

	// Console, with the credentials broker's pages mounted on it.
	consoleCfg := console.Config{PublicURL: s.ConsoleURL, Token: randomToken(), Logger: cfg.Logger}
	if cfg.SlackWebhook != "" {
		consoleCfg.Notifier = console.SlackWebhook{URL: cfg.SlackWebhook}
	}
	s.Console = console.New(consoleCfg)
	broker, err := s.broker(cfg)
	if err != nil {
		s.Close()
		return nil, err
	}
	s.serve(conL, s.Console.Handler())
	observe := func(agentURL string) agentkit.Observe {
		return agentkit.Observe{PushURL: s.Console.PushURL(agentURL), Token: s.Console.Token()}
	}

	// Suppliers. Contoso is cheaper but slower: whether it wins depends on the deadline.
	for _, sup := range []struct {
		l   net.Listener
		cfg supplier.Config
	}{
		{nwL, supplier.Config{Name: "Northwind Merch", BaseURL: s.NorthwindURL, UnitPrice: 28.50, LeadDays: 7, Step: cfg.FulfilmentStep}},
		{coL, supplier.Config{Name: "Contoso Prints", BaseURL: s.ContosoURL, UnitPrice: 24.00, LeadDays: 30, Step: cfg.FulfilmentStep}},
	} {
		agent := supplier.New(sup.cfg)
		s.serve(sup.l, agentkit.Mux(agent.Card(), agent, observe(sup.cfg.BaseURL)))
	}

	// Buyer.
	registry := regclient.New(s.RegistryURL, nil)
	b := buyer.New(buyer.Config{BaseURL: s.BuyerURL, Registry: registry, Broker: broker, Integration: cfg.Integration})
	buyerMux := agentkit.Mux(b.Card(), b, observe(s.BuyerURL))
	buyerMux.Handle(buyer.WebhookPath, b.Webhook())
	s.serve(buyL, buyerMux)

	for _, u := range []string{s.NorthwindURL, s.ContosoURL, s.BuyerURL} {
		if _, err := registry.Register(ctx, u); err != nil {
			s.Close()
			return nil, err
		}
	}
	return s, nil
}

func (s *Stack) broker(cfg Config) (credentials.Broker, error) {
	switch cfg.Credentials {
	case CredentialsNone:
		return nil, nil
	case CredentialsDemo:
		s.DemoBroker = demo.New(s.ConsoleURL + "/demo-auth")
		s.Console.Mount("/demo-auth/", s.DemoBroker.Handler(), "Demo calendar", "/demo-auth/calendar")
		return s.DemoBroker, nil
	case CredentialsNango:
		client, err := nango.NewClient(cfg.NangoSecretKey, cfg.NangoBaseURL, nil)
		if err != nil {
			return nil, err
		}
		b := nango.NewBroker(client, nango.BrokerConfig{WebhookSigningKey: cfg.NangoWebhookSigningKey, Logger: cfg.Logger})
		// Reachable by Nango only through a public URL (a tunnel); polling covers local runs.
		s.Console.Mount("/webhooks/nango/", b.WebhookHandler(), "", "")
		return b, nil
	}
	return nil, fmt.Errorf("unknown credentials mode %q (want demo, nango or none)", cfg.Credentials)
}

func (s *Stack) serve(l net.Listener, h http.Handler) {
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	s.servers = append(s.servers, srv)
	go func() {
		if err := srv.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server stopped", "addr", l.Addr(), "error", err)
		}
	}()
}

// Close stops every server at once. Everything is in memory, so there is nothing a
// graceful drain would save.
func (s *Stack) Close() {
	for _, srv := range s.servers {
		_ = srv.Close()
	}
}

func randomToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
