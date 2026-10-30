// Package buyer is Acme's procurement agent, an orchestrator that delegates to other
// agents. For a request like "Order 200 hoodies, budget 6000 EUR, deliver by
// 2026-11-20, add to my calendar" it:
//
//  1. finds suppliers in the registry by skill and asks each for a quote, in parallel
//  2. picks the cheapest quote that ships in time, saved as a quote-comparison artifact
//  3. above the approval limit, goes INPUT_REQUIRED with a structuredinput schema, so a
//     person can approve in the console's inbox (or any A2A client can answer)
//  4. places the order with returnImmediately + a push config, records the supplier's task
//     as its child (package links) and waits for the supplier's webhook
//  5. if asked, adds the delivery date to the requester's calendar. Without a connection
//     it goes AUTH_REQUIRED with a connect link and continues once the user has connected
//     (spec 7.6.1); the calendar is called through the credentials broker
//  6. finishes with a purchase-order artifact
package buyer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
	"github.com/a2aproject/a2a-go/v2/a2aext/structuredinput"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/three-agent-problem/examples/procurement/internal/agentkit"
	"github.com/three-agent-problem/examples/procurement/protocol"
	"github.com/three-agent-problem/lagrange/console"
	"github.com/three-agent-problem/lagrange/credentials"
	"github.com/three-agent-problem/lagrange/links"
	"github.com/three-agent-problem/registry"
	regclient "github.com/three-agent-problem/registry/client"
)

// WebhookPath receives the suppliers' push notifications.
const WebhookPath = "/webhook"

// Config configures the buyer.
type Config struct {
	BaseURL  string
	Registry *regclient.Client
	// Broker is used for the calendar step. Nil skips it.
	Broker credentials.Broker
	// Integration is the broker's integration ID for Google Calendar.
	Integration string
	// ApprovalLimit is the order total above which a person must approve. Default 5000 EUR.
	ApprovalLimit float64
	// DefaultUser is the requester when the A2A call is not authenticated.
	DefaultUser string
	// OrderTimeout bounds the wait for a supplier. Default 2 minutes.
	OrderTimeout time.Duration
	// AuthTimeout bounds the wait for the user to connect their calendar. Default 10 minutes.
	AuthTimeout time.Duration
	// WebhookURL is where suppliers push order updates. Default BaseURL + WebhookPath.
	// On Lagrange it is the agent's tunnel passthrough, <server>/t/<handle>/webhook.
	WebhookURL string
	// CallInterceptors are added to the clients that call suppliers, for example
	// delegate.Interceptor(token) on Lagrange.
	CallInterceptors []a2aclient.CallInterceptor
}

// Agent is the buyer agent.
type Agent struct {
	cfg          Config
	webhookToken string
	orders       *tracker

	mu      sync.Mutex
	clients map[string]*a2aclient.Client
}

var _ a2asrv.AgentExecutor = (*Agent)(nil)

// New creates the buyer agent.
func New(cfg Config) *Agent {
	if cfg.ApprovalLimit <= 0 {
		cfg.ApprovalLimit = 5000
	}
	if cfg.Integration == "" {
		cfg.Integration = "google-calendar"
	}
	if cfg.DefaultUser == "" {
		cfg.DefaultUser = "demo-user"
	}
	if cfg.OrderTimeout <= 0 {
		cfg.OrderTimeout = 2 * time.Minute
	}
	if cfg.AuthTimeout <= 0 {
		cfg.AuthTimeout = 10 * time.Minute
	}
	cfg.BaseURL = strings.TrimSuffix(cfg.BaseURL, "/")
	if cfg.WebhookURL == "" {
		cfg.WebhookURL = cfg.BaseURL + WebhookPath
	}
	return &Agent{cfg: cfg, webhookToken: randomID(16), orders: newTracker(), clients: make(map[string]*a2aclient.Client)}
}

// Card returns the buyer's agent card.
func (a *Agent) Card() *a2a.AgentCard {
	return agentkit.Card("Acme Procurement", "Buys merchandise for Acme: compares supplier quotes, gets approvals, places and tracks orders.", a.cfg.BaseURL,
		a2a.AgentCapabilities{
			Streaming:         true,
			PushNotifications: true,
			Extensions:        []a2a.AgentExtension{structuredinput.NewExtension(structuredinput.Draft202012)},
		},
		a2a.AgentSkill{
			ID: "purchase", Name: "Purchase", Tags: []string{"procurement", "purchase"},
			Description: "Buys goods from registered suppliers, asking for approval above the limit.",
			Examples:    []string{"Order 200 hoodies, budget 6000 EUR, deliver by 2026-11-20, add to my calendar"},
		},
	)
}

// Webhook receives the suppliers' push notifications for the orders this agent placed.
func (a *Agent) Webhook() http.Handler { return a.orders.handler(a.webhookToken) }

// Execute implements a2asrv.AgentExecutor.
func (a *Agent) Execute(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		if ec.StoredTask == nil {
			a.start(ctx, ec, yield)
			return
		}
		a.resume(ctx, ec, yield)
	}
}

// Cancel implements a2asrv.AgentExecutor.
func (a *Agent) Cancel(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCanceled, nil), nil)
	}
}

var approvalSchema = &jsonschema.Schema{
	Type:  "object",
	Title: "order_approval",
	Properties: map[string]*jsonschema.Schema{
		"approved": {Type: "boolean", Title: "Approve the order"},
		"note":     {Type: "string", Title: "Note"},
	},
	Required: []string{"approved"},
}

type approval struct {
	Approved bool   `json:"approved"`
	Note     string `json:"note"`
}

func (a *Agent) start(ctx context.Context, ec *a2asrv.ExecutorContext, yield func(a2a.Event, error) bool) {
	purchase, err := parsePurchase(ec.Message)
	if err != nil {
		msg := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(err.Error()))
		msg.ContextID = ec.ContextID
		yield(msg, nil)
		return
	}
	if !yield(a2a.NewSubmittedTask(ec, ec.Message), nil) {
		return
	}
	if !a.status(ec, a2a.TaskStateWorking, yield, fmt.Sprintf("Looking for suppliers of %d %ss.", purchase.Quantity, purchase.Item)) {
		return
	}

	plan, err := a.compareQuotes(ctx, purchase)
	if err != nil {
		a.status(ec, a2a.TaskStateFailed, yield, err.Error())
		return
	}
	comparison := a2a.NewArtifactEvent(ec, a2a.NewTextPart(describePlan(plan)), protocol.DataPart(plan))
	comparison.Artifact.Name = protocol.ArtifactQuoteComparison
	if !yield(comparison, nil) {
		return
	}
	if plan.Chosen == nil {
		a.status(ec, a2a.TaskStateFailed, yield, plan.Reason)
		return
	}

	if plan.NeedsApproval {
		req := &structuredinput.Request{ID: "approve-" + plan.Chosen.QuoteID, Schema: approvalSchema}
		yield(structuredinput.NewRequestEvent(ec, req,
			a2a.NewTextPart(approvalQuestion(plan, a.cfg.ApprovalLimit)),
			protocol.DataPart(plan.Chosen),
		))
		return
	}
	a.order(ctx, ec, plan, fmt.Sprintf("auto-approved (under %.0f %s)", a.cfg.ApprovalLimit, plan.Chosen.Currency), yield)
}

func (a *Agent) resume(ctx context.Context, ec *a2asrv.ExecutorContext, yield func(a2a.Event, error) bool) {
	req, err := structuredinput.PendingRequest(ec.StoredTask)
	if err != nil || req == nil {
		yield(nil, fmt.Errorf("this task is not waiting for an approval: %w", a2a.ErrInvalidParams))
		return
	}
	var processor structuredinput.Processor
	result, err := processor.Process(ctx, req, ec.Message)
	if err != nil {
		yield(nil, err)
		return
	}
	if !result.Accepted() {
		yield(result.NewRetryEvent(ec, a2a.NewTextPart(`Answer with {"approved": true|false, "note": "..."}.`)))
		return
	}
	var answer approval
	if err := result.Decode(&answer); err != nil {
		yield(nil, err)
		return
	}

	plan, ok := planFrom(ec.StoredTask)
	if !ok || plan.Chosen == nil {
		a.status(ec, a2a.TaskStateFailed, yield, "The quote comparison is missing from the task.")
		return
	}
	approver := a.approver(ec)
	if !answer.Approved {
		a.status(ec, a2a.TaskStateRejected, yield, fmt.Sprintf("Order rejected by %s. %s", approver, answer.Note))
		return
	}
	approvedBy := approver
	if answer.Note != "" {
		approvedBy += " (" + answer.Note + ")"
	}
	if !a.status(ec, a2a.TaskStateWorking, yield, "Approved by "+approvedBy+".") {
		return
	}
	a.order(ctx, ec, plan, approvedBy, yield)
}

// approver names who answered the approval: the console's audit metadata, else the caller.
func (a *Agent) approver(ec *a2asrv.ExecutorContext) string {
	if by, _ := ec.Message.Metadata[console.AnsweredByKey].(string); by != "" {
		return by
	}
	return a.user(ec)
}

func (a *Agent) user(ec *a2asrv.ExecutorContext) string {
	if ec.User != nil && ec.User.Name != "" {
		return ec.User.Name
	}
	return a.cfg.DefaultUser
}

// compareQuotes asks every supplier with the quote skill for a price and picks one.
func (a *Agent) compareQuotes(ctx context.Context, p protocol.Purchase) (protocol.Plan, error) {
	entries, err := a.cfg.Registry.Search(ctx, registry.Query{Skill: "quote"})
	if err != nil {
		return protocol.Plan{}, fmt.Errorf("the registry is unreachable: %w", err)
	}
	if len(entries) == 0 {
		return protocol.Plan{}, errors.New("no supplier with the quote skill is registered")
	}

	type result struct {
		quote protocol.Quote
		err   error
	}
	results := make([]result, len(entries))
	var wg sync.WaitGroup
	for i, e := range entries {
		wg.Go(func() {
			q, err := a.requestQuote(ctx, e.URL, p)
			results[i] = result{q, err}
		})
	}
	wg.Wait()

	plan := protocol.Plan{Purchase: p}
	var failures []string
	for i, r := range results {
		if r.err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", entries[i].Name(), r.err))
			continue
		}
		plan.Quotes = append(plan.Quotes, r.quote)
	}
	choose(&plan, a.cfg.ApprovalLimit)
	if len(failures) > 0 {
		plan.Reason += " Unreachable: " + strings.Join(failures, "; ") + "."
	}
	return plan, nil
}

// choose picks the cheapest quote that ships by the deadline.
func choose(plan *protocol.Plan, limit float64) {
	var eligible []protocol.Quote
	var late []string
	for _, q := range plan.Quotes {
		if plan.Purchase.DeliverBy != "" && q.ShipDate > plan.Purchase.DeliverBy {
			late = append(late, fmt.Sprintf("%s ships %s, after %s", q.Supplier, q.ShipDate, plan.Purchase.DeliverBy))
			continue
		}
		eligible = append(eligible, q)
	}
	if len(eligible) == 0 {
		plan.Reason = "No supplier can deliver in time. " + strings.Join(late, "; ") + "."
		return
	}
	best := slices.MinFunc(eligible, func(x, y protocol.Quote) int {
		switch {
		case x.Total < y.Total:
			return -1
		case x.Total > y.Total:
			return 1
		}
		return strings.Compare(x.Supplier, y.Supplier)
	})
	plan.Chosen = &best
	plan.Reason = fmt.Sprintf("%s is the cheapest of %d quote(s) that ship in time.", best.Supplier, len(eligible))
	if len(late) > 0 {
		plan.Reason += " Excluded: " + strings.Join(late, "; ") + "."
	}
	plan.NeedsApproval = best.Total > limit || (plan.Purchase.Budget > 0 && best.Total > plan.Purchase.Budget)
}

func (a *Agent) requestQuote(ctx context.Context, agentURL string, p protocol.Purchase) (protocol.Quote, error) {
	client, err := a.client(ctx, agentURL)
	if err != nil {
		return protocol.Quote{}, err
	}
	msg := a2a.NewMessage(a2a.MessageRoleUser, protocol.DataPart(protocol.Request{
		Request: protocol.KindQuote, Item: p.Item, Quantity: p.Quantity, DeliverBy: p.DeliverBy, Buyer: "Acme",
	}))
	res, err := client.SendMessage(ctx, &a2a.SendMessageRequest{Message: msg})
	if err != nil {
		return protocol.Quote{}, err
	}
	reply, ok := res.(*a2a.Message)
	if !ok {
		return protocol.Quote{}, fmt.Errorf("expected a message with a quote, got %T", res)
	}
	var q protocol.Quote
	if !protocol.Decode(reply.Parts, &q) || q.QuoteID == "" {
		return protocol.Quote{}, errors.New("the reply has no quote")
	}
	return q, nil
}

// order places the order, waits for the supplier, adds the calendar entry and finishes.
func (a *Agent) order(ctx context.Context, ec *a2asrv.ExecutorContext, plan protocol.Plan, approvedBy string, yield func(a2a.Event, error) bool) {
	q := plan.Chosen
	client, err := a.client(ctx, q.AgentURL)
	if err != nil {
		a.status(ec, a2a.TaskStateFailed, yield, "The supplier is unreachable: "+err.Error())
		return
	}
	msg := a2a.NewMessage(a2a.MessageRoleUser, protocol.DataPart(protocol.Request{
		Request: protocol.KindOrder, QuoteID: q.QuoteID, Item: q.Item, Quantity: q.Quantity, DeliverBy: plan.Purchase.DeliverBy, Buyer: "Acme",
	}))
	res, err := client.SendMessage(ctx, &a2a.SendMessageRequest{
		Message: msg,
		Config: &a2a.SendMessageConfig{
			ReturnImmediately: true,
			PushConfig:        &a2a.PushConfig{URL: a.cfg.WebhookURL, Token: a.webhookToken},
		},
	})
	if err != nil {
		a.status(ec, a2a.TaskStateFailed, yield, "The supplier did not accept the order: "+err.Error())
		return
	}
	supplierTask, ok := res.(*a2a.Task)
	if !ok {
		a.status(ec, a2a.TaskStateFailed, yield, fmt.Sprintf("The supplier answered the order with a %T instead of a task.", res))
		return
	}

	placed := a2a.NewStatusUpdateEvent(ec, a2a.TaskStateWorking, a2a.NewMessageForTask(a2a.MessageRoleAgent, ec,
		a2a.NewTextPart(fmt.Sprintf("Order placed with %s (task …%s). Waiting for fulfilment updates.", q.Supplier, tail(string(supplierTask.ID))))))
	links.Set(placed, links.Child{AgentURL: q.AgentURL, TaskID: supplierTask.ID, Skill: "order"})
	if !yield(placed, nil) {
		return
	}

	waitCtx, cancel := context.WithTimeout(ctx, a.cfg.OrderTimeout)
	state, err := a.orders.wait(waitCtx, supplierTask.ID)
	cancel()
	if err != nil {
		a.status(ec, a2a.TaskStateFailed, yield, "No word from the supplier: "+err.Error())
		return
	}
	if state != a2a.TaskStateCompleted {
		a.status(ec, a2a.TaskStateFailed, yield, fmt.Sprintf("The supplier ended the order as %s.", state))
		return
	}
	final, err := client.GetTask(ctx, &a2a.GetTaskRequest{ID: supplierTask.ID})
	if err != nil {
		a.status(ec, a2a.TaskStateFailed, yield, "Could not read the supplier's confirmation: "+err.Error())
		return
	}
	po := protocol.PurchaseOrder{
		PONumber: "PO-" + strings.ToUpper(randomID(3)), Supplier: q.Supplier, Item: q.Item, Quantity: q.Quantity,
		Total: q.Total, Currency: q.Currency, ApprovedBy: approvedBy, SupplierTaskID: supplierTask.ID,
	}
	if art := protocol.FindArtifact(final.Artifacts, protocol.ArtifactOrderConfirmation); art != nil {
		var c protocol.OrderConfirmation
		if protocol.Decode(art.Parts, &c) {
			po.Confirmation = &c
		}
	}

	if plan.Purchase.AddToCalendar {
		if !a.addToCalendar(ctx, ec, &po, yield) {
			return
		}
	}

	result := a2a.NewArtifactEvent(ec, a2a.NewTextPart(describePO(po)), protocol.DataPart(po))
	result.Artifact.Name = protocol.ArtifactPurchaseOrder
	if !yield(result, nil) {
		return
	}
	a.status(ec, a2a.TaskStateCompleted, yield, describePO(po))
}

// addToCalendar creates the delivery event through the credentials broker, asking the
// user to connect their calendar first if needed. A failure here does not fail the
// purchase; it is noted on the purchase order. It returns false only if the client went away.
func (a *Agent) addToCalendar(ctx context.Context, ec *a2asrv.ExecutorContext, po *protocol.PurchaseOrder, yield func(a2a.Event, error) bool) bool {
	if a.cfg.Broker == nil {
		po.CalendarNote = "No credentials broker is configured, so the calendar entry was skipped."
		return true
	}
	user := a.user(ec)
	grant, err := a.cfg.Broker.Lookup(ctx, user, a.cfg.Integration)
	if errors.Is(err, credentials.ErrNoGrant) {
		challenge, err := a.cfg.Broker.Begin(ctx, user, a.cfg.Integration)
		if err != nil {
			po.CalendarNote = "Could not start the calendar connection: " + err.Error()
			return true
		}
		ask := a2a.NewStatusUpdateEvent(ec, a2a.TaskStateAuthRequired, a2a.NewMessageForTask(a2a.MessageRoleAgent, ec,
			credentials.NewChallengeParts(challenge, "The order has shipped. To put the delivery date in your calendar I need access to it.")...))
		if !yield(ask, nil) {
			return false
		}
		// a2a-go keeps consuming events after AUTH_REQUIRED, so the executor can wait here
		// and continue by itself once the user has connected (spec 7.6.1).
		waitCtx, cancel := context.WithTimeout(ctx, a.cfg.AuthTimeout)
		grant, err = a.cfg.Broker.Await(waitCtx, challenge)
		cancel()
		if err != nil {
			po.CalendarNote = "Calendar entry skipped: " + err.Error()
			return a.status(ec, a2a.TaskStateWorking, yield, po.CalendarNote)
		}
		if !a.status(ec, a2a.TaskStateWorking, yield, "Calendar connected. Adding the delivery date.") {
			return false
		}
	} else if err != nil {
		po.CalendarNote = "Could not check the calendar connection: " + err.Error()
		return true
	}

	if po.Confirmation == nil {
		po.CalendarNote = "The supplier sent no ship date, so there is nothing to put in the calendar."
		return true
	}
	date := po.Confirmation.ShipDate
	day, _ := time.Parse(protocol.DateLayout, date)
	event := map[string]any{
		"summary":     fmt.Sprintf("Delivery: %d %ss from %s", po.Quantity, po.Item, po.Supplier),
		"description": fmt.Sprintf("%s, order %s, tracking %s", po.PONumber, po.Confirmation.OrderID, po.Confirmation.Tracking),
		"start":       map[string]string{"date": date},
		"end":         map[string]string{"date": protocol.AddDays(day, 1)},
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := grant.Call(ctx, http.MethodPost, "/calendar/v3/calendars/primary/events", event, &created); err != nil {
		po.CalendarNote = "The calendar rejected the event: " + err.Error()
		return true
	}
	po.CalendarEventID = created.ID
	return true
}

// status yields a status update with a text message. It returns false if the client went away.
func (a *Agent) status(ec *a2asrv.ExecutorContext, state a2a.TaskState, yield func(a2a.Event, error) bool, text string) bool {
	return yield(a2a.NewStatusUpdateEvent(ec, state, a2a.NewMessageForTask(a2a.MessageRoleAgent, ec, a2a.NewTextPart(text))), nil)
}

// client returns a cached A2A client for the agent at agentURL. The card is always read
// from the agent itself, never from the registry's copy.
func (a *Agent) client(ctx context.Context, agentURL string) (*a2aclient.Client, error) {
	a.mu.Lock()
	c, ok := a.clients[agentURL]
	a.mu.Unlock()
	if ok {
		return c, nil
	}
	card, err := agentcard.DefaultResolver.Resolve(ctx, agentURL)
	if err != nil {
		return nil, err
	}
	c, err = a2aclient.NewFromCard(ctx, card, a2aclient.WithCallInterceptors(a.cfg.CallInterceptors...))
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.clients[agentURL] = c
	a.mu.Unlock()
	return c, nil
}

func planFrom(task *a2a.Task) (protocol.Plan, bool) {
	art := protocol.FindArtifact(task.Artifacts, protocol.ArtifactQuoteComparison)
	if art == nil {
		return protocol.Plan{}, false
	}
	var plan protocol.Plan
	return plan, protocol.Decode(art.Parts, &plan)
}

func describePlan(plan protocol.Plan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Quotes for %d %ss", plan.Purchase.Quantity, plan.Purchase.Item)
	if plan.Purchase.DeliverBy != "" {
		fmt.Fprintf(&b, " by %s", plan.Purchase.DeliverBy)
	}
	b.WriteString(": ")
	for i, q := range plan.Quotes {
		if i > 0 {
			b.WriteString("; ")
		}
		fmt.Fprintf(&b, "%s %.2f %s (%.2f each, ships %s)", q.Supplier, q.Total, q.Currency, q.UnitPrice, q.ShipDate)
	}
	b.WriteString(". " + plan.Reason)
	return b.String()
}

func approvalQuestion(plan protocol.Plan, limit float64) string {
	q := plan.Chosen
	why := fmt.Sprintf("above the %.0f %s approval limit", limit, q.Currency)
	if plan.Purchase.Budget > 0 && q.Total > plan.Purchase.Budget {
		why = fmt.Sprintf("over the requested budget of %.0f %s", plan.Purchase.Budget, q.Currency)
	}
	return fmt.Sprintf("Approve %d %ss from %s for %.2f %s (ships %s)? The total is %s. %s",
		q.Quantity, q.Item, q.Supplier, q.Total, q.Currency, q.ShipDate, why, plan.Reason)
}

func describePO(po protocol.PurchaseOrder) string {
	text := fmt.Sprintf("%s: %d %ss from %s for %.2f %s, approved by %s.", po.PONumber, po.Quantity, po.Item, po.Supplier, po.Total, po.Currency, po.ApprovedBy)
	if c := po.Confirmation; c != nil {
		text += fmt.Sprintf(" Shipped as %s, tracking %s, arrives %s.", c.OrderID, c.Tracking, c.ShipDate)
	}
	if po.CalendarEventID != "" {
		text += " Added to your calendar."
	} else if po.CalendarNote != "" {
		text += " " + po.CalendarNote
	}
	return text
}

func tail(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[len(s)-8:]
}

func randomID(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
