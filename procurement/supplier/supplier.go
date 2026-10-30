// Package supplier is a merchandise supplier's agent. It does two things:
//
//   - quote: answer at once with a plain Message (no task), pattern 1 in docs/a2a-patterns.md
//   - order: run a task that streams fulfilment progress as artifact updates and ends
//     with an order confirmation. Callers usually send it with returnImmediately and a
//     push config, then wait for the webhook (pattern 5).
package supplier

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"iter"
	"math"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/three-agent-problem/examples/procurement/internal/agentkit"
	"github.com/three-agent-problem/examples/procurement/protocol"
)

// Config describes one supplier.
type Config struct {
	Name      string
	BaseURL   string
	UnitPrice float64
	LeadDays  int
	// Step is the simulated time between fulfilment updates.
	Step time.Duration
}

// Agent is a supplier agent.
type Agent struct {
	cfg Config
}

var _ a2asrv.AgentExecutor = (*Agent)(nil)

// New creates a supplier agent.
func New(cfg Config) *Agent {
	if cfg.Step <= 0 {
		cfg.Step = time.Second
	}
	return &Agent{cfg: cfg}
}

// Card returns the supplier's agent card.
func (a *Agent) Card() *a2a.AgentCard {
	return agentkit.Card(a.cfg.Name, fmt.Sprintf("%s quotes and fulfils branded merchandise orders.", a.cfg.Name), a.cfg.BaseURL,
		a2a.AgentCapabilities{Streaming: true, PushNotifications: true},
		a2a.AgentSkill{
			ID: "quote", Name: "Quote", Tags: []string{"quote", "merch"},
			Description: `Prices an order. Send a data part {"request":"quote","item":"hoodie","quantity":200,"deliverBy":"2026-11-20"}.`,
			InputModes:  []string{"application/json"}, OutputModes: []string{"application/json"},
		},
		a2a.AgentSkill{
			ID: "order", Name: "Order", Tags: []string{"order", "merch"},
			Description: `Places an order against a quote and reports fulfilment. Send {"request":"order","quoteId":"Q-…","item":"hoodie","quantity":200}.`,
			InputModes:  []string{"application/json"}, OutputModes: []string{"application/json", "text/plain"},
		},
	)
}

// Execute implements a2asrv.AgentExecutor.
func (a *Agent) Execute(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		var req protocol.Request
		if !protocol.Decode(ec.Message.Parts, &req) {
			yield(a.reply(ec, a2a.NewTextPart(`Send a data part with "request": "quote" or "order".`)), nil)
			return
		}
		switch req.Request {
		case protocol.KindQuote:
			yield(a.reply(ec, protocol.DataPart(a.quote(req, time.Now()))), nil)
		case protocol.KindOrder:
			a.fulfil(ctx, ec, req, yield)
		default:
			yield(a.reply(ec, a2a.NewTextPart(fmt.Sprintf("Unknown request %q.", req.Request))), nil)
		}
	}
}

// Cancel implements a2asrv.AgentExecutor.
func (a *Agent) Cancel(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCanceled, nil), nil)
	}
}

func (a *Agent) reply(ec *a2asrv.ExecutorContext, parts ...*a2a.Part) *a2a.Message {
	msg := a2a.NewMessage(a2a.MessageRoleAgent, parts...)
	msg.ContextID = ec.ContextID
	return msg
}

// quote prices a request. Orders of 150 units or more get 5% off.
func (a *Agent) quote(req protocol.Request, now time.Time) protocol.Quote {
	unit := a.cfg.UnitPrice
	if req.Quantity >= 150 {
		unit = math.Round(unit*0.95*100) / 100
	}
	return protocol.Quote{
		QuoteID:    "Q-" + randomID(),
		Supplier:   a.cfg.Name,
		AgentURL:   strings.TrimSuffix(a.cfg.BaseURL, "/"),
		Item:       req.Item,
		Quantity:   req.Quantity,
		UnitPrice:  unit,
		Total:      math.Round(unit*float64(req.Quantity)*100) / 100,
		Currency:   "EUR",
		LeadDays:   a.cfg.LeadDays,
		ShipDate:   protocol.AddDays(now, a.cfg.LeadDays),
		ValidUntil: protocol.AddDays(now, 7),
	}
}

func (a *Agent) fulfil(ctx context.Context, ec *a2asrv.ExecutorContext, req protocol.Request, yield func(a2a.Event, error) bool) {
	if ec.StoredTask == nil {
		if !yield(a2a.NewSubmittedTask(ec, ec.Message), nil) {
			return
		}
	}
	text := fmt.Sprintf("Order received: %d × %s against quote %s.", req.Quantity, req.Item, req.QuoteID)
	if !yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateWorking, a2a.NewMessageForTask(a2a.MessageRoleAgent, ec, a2a.NewTextPart(text))), nil) {
		return
	}

	// The fulfilment log is one artifact that grows chunk by chunk (spec 4.2, append/lastChunk).
	steps := []string{"Production scheduled.", "Printed and quality-checked.", "Packed and handed to the carrier."}
	first := a2a.NewArtifactEvent(ec, a2a.NewTextPart(steps[0]))
	first.Artifact.Name = protocol.ArtifactFulfilment
	if !yield(first, nil) {
		return
	}
	for i, step := range steps[1:] {
		if !sleep(ctx, a.cfg.Step) {
			yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCanceled, nil), nil)
			return
		}
		ev := a2a.NewArtifactUpdateEvent(ec, first.Artifact.ID, a2a.NewTextPart(step))
		ev.LastChunk = i == len(steps)-2
		if !yield(ev, nil) {
			return
		}
	}

	quote := a.quote(req, time.Now())
	confirmation := protocol.OrderConfirmation{
		OrderID:  "O-" + randomID(),
		QuoteID:  req.QuoteID,
		Total:    quote.Total,
		Currency: quote.Currency,
		ShipDate: quote.ShipDate,
		Tracking: "TRK" + strings.ToUpper(randomID()),
	}
	conf := a2a.NewArtifactEvent(ec, protocol.DataPart(confirmation))
	conf.Artifact.Name = protocol.ArtifactOrderConfirmation
	if !yield(conf, nil) {
		return
	}
	done := fmt.Sprintf("Shipped. Order %s, tracking %s, arrives %s.", confirmation.OrderID, confirmation.Tracking, confirmation.ShipDate)
	yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCompleted, a2a.NewMessageForTask(a2a.MessageRoleAgent, ec, a2a.NewTextPart(done))), nil)
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func randomID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
