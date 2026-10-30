package procurement_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
	"github.com/three-agent-problem/examples/procurement/protocol"
	"github.com/three-agent-problem/examples/procurement/stack"
	"github.com/three-agent-problem/lagrange/credentials"
)

func startStack(t *testing.T) (*stack.Stack, *a2aclient.Client) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s, err := stack.Start(ctx, stack.Config{Host: "127.0.0.1", Credentials: stack.CredentialsDemo, FulfilmentStep: 20 * time.Millisecond})
	if err != nil {
		t.Fatalf("stack.Start() error = %v", err)
	}
	t.Cleanup(s.Close)
	card, err := agentcard.DefaultResolver.Resolve(ctx, s.BuyerURL)
	if err != nil {
		t.Fatal(err)
	}
	client, err := a2aclient.NewFromCard(ctx, card)
	if err != nil {
		t.Fatal(err)
	}
	return s, client
}

func send(t *testing.T, client *a2aclient.Client, text string) *a2a.Task {
	t.Helper()
	res, err := client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(text))})
	if err != nil {
		t.Fatalf("SendMessage() error = %v", err)
	}
	task, ok := res.(*a2a.Task)
	if !ok {
		t.Fatalf("SendMessage() = %T, want a task", res)
	}
	return task
}

// waitState polls the buyer until the task reaches state.
func waitState(t *testing.T, client *a2aclient.Client, id a2a.TaskID, state a2a.TaskState) *a2a.Task {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		task, err := client.GetTask(context.Background(), &a2a.GetTaskRequest{ID: id})
		if err != nil {
			t.Fatalf("GetTask() error = %v", err)
		}
		if task.Status.State == state {
			return task
		}
		if task.Status.State.Terminal() || time.Now().After(deadline) {
			t.Fatalf("task is %s (%v), want %s", task.Status.State, statusText(task), state)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func answer(t *testing.T, s *stack.Stack, id a2a.TaskID, approved string) {
	t.Helper()
	// The console learns about the task from push notifications; wait until it has it.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if ct, ok := s.Console.Task(id); ok && ct.State == a2a.TaskStateInputRequired {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the console never saw the task waiting for input")
		}
		time.Sleep(10 * time.Millisecond)
	}
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noRedirect.PostForm(s.ConsoleURL+"/inbox/"+string(id), url.Values{
		"approved": {approved}, "note": {"offsite merch"}, "_answered_by": {"finance@acme.example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/tasks/") {
		t.Fatalf("inbox answer redirected to %q, want the task page (an error went to the inbox)", loc)
	}
}

func TestPurchaseWithApprovalAndCalendar(t *testing.T) {
	s, client := startStack(t)
	deliverBy := time.Now().AddDate(0, 0, 21).Format(protocol.DateLayout)

	task := send(t, client, "Order 200 hoodies, budget 6000 EUR, deliver by "+deliverBy+", add to my calendar")
	if task.Status.State != a2a.TaskStateInputRequired {
		t.Fatalf("first state = %s (%s), want INPUT_REQUIRED for approval", task.Status.State, statusText(task))
	}
	if text := statusText(task); !strings.Contains(text, "Northwind Merch") {
		t.Errorf("approval question = %q, want Northwind (Contoso ships too late)", text)
	}

	answer(t, s, task.ID, "true")

	authTask := waitState(t, client, task.ID, a2a.TaskStateAuthRequired)
	challenge, ok := credentials.ChallengeFromMessage(authTask.Status.Message)
	if !ok {
		t.Fatalf("AUTH_REQUIRED status has no challenge: %q", statusText(authTask))
	}
	if err := s.DemoBroker.Decide(challenge.ID, true); err != nil {
		t.Fatal(err)
	}

	done := waitState(t, client, task.ID, a2a.TaskStateCompleted)
	art := protocol.FindArtifact(done.Artifacts, protocol.ArtifactPurchaseOrder)
	if art == nil {
		t.Fatalf("completed task has no purchase-order artifact; artifacts: %d", len(done.Artifacts))
	}
	var po protocol.PurchaseOrder
	if !protocol.Decode(art.Parts, &po) {
		t.Fatal("purchase-order artifact has no data")
	}
	if po.Supplier != "Northwind Merch" || po.Quantity != 200 || po.Confirmation == nil || po.CalendarEventID == "" {
		t.Errorf("purchase order = %+v", po)
	}
	if !strings.HasPrefix(po.ApprovedBy, "finance@acme.example") {
		t.Errorf("approved by = %q, want the inbox user", po.ApprovedBy)
	}
	events := s.DemoBroker.Events()
	if len(events) != 1 || events[0].Start.Date != po.Confirmation.ShipDate {
		t.Errorf("calendar events = %+v, want one on %s", events, po.Confirmation.ShipDate)
	}

	// The console saw the buyer delegate to the supplier, and the supplier's task completed.
	ct, ok := s.Console.Task(task.ID)
	if !ok || len(ct.Children) != 1 {
		t.Fatalf("console task = %+v, want one child", ct)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		child, ok := s.Console.Task(ct.Children[0])
		if ok && child.State == a2a.TaskStateCompleted && child.ParentID == task.ID {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("console child = %+v, want the completed supplier task", child)
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, state := range []a2a.TaskState{a2a.TaskStateInputRequired, a2a.TaskStateAuthRequired} {
		if ct, _ := s.Console.Task(task.ID); ct.TimeIn[state] <= 0 {
			t.Errorf("console time in %s = %v, want > 0", state, ct.TimeIn[state])
		}
	}

	// A second purchase reuses the calendar connection: no AUTH_REQUIRED this time.
	second := send(t, client, "Order 200 hoodies, budget 6000 EUR, deliver by "+deliverBy+", add to my calendar")
	answer(t, s, second.ID, "true")
	waitState(t, client, second.ID, a2a.TaskStateCompleted)
	if got := len(s.DemoBroker.Events()); got != 2 {
		t.Errorf("calendar events after the second purchase = %d, want 2", got)
	}
}

func TestRejectedPurchase(t *testing.T) {
	s, client := startStack(t)
	deliverBy := time.Now().AddDate(0, 0, 21).Format(protocol.DateLayout)
	task := send(t, client, "Order 200 hoodies, deliver by "+deliverBy)
	answer(t, s, task.ID, "false")
	rejected := waitState(t, client, task.ID, a2a.TaskStateRejected)
	if text := statusText(rejected); !strings.Contains(text, "finance@acme.example") {
		t.Errorf("rejection = %q, want it to name who rejected", text)
	}
}

func TestSmallOrderIsAutoApproved(t *testing.T) {
	_, client := startStack(t)
	// Without a deadline the cheaper, slower supplier wins and stays under the limit.
	task := send(t, client, "Order 50 mugs")
	if task.Status.State != a2a.TaskStateCompleted {
		t.Fatalf("state = %s (%s), want COMPLETED without approval", task.Status.State, statusText(task))
	}
	if text := statusText(task); !strings.Contains(text, "Contoso Prints") || !strings.Contains(text, "auto-approved") {
		t.Errorf("result = %q, want an auto-approved Contoso order", text)
	}
}

func TestUnclearRequestGetsAMessage(t *testing.T) {
	_, client := startStack(t)
	res, err := client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hello"))})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := res.(*a2a.Message); !ok {
		t.Fatalf("SendMessage() = %T, want a plain message asking to rephrase", res)
	}
}

func TestInboxShowsApproval(t *testing.T) {
	s, client := startStack(t)
	task := send(t, client, "Order 200 hoodies, deliver by "+time.Now().AddDate(0, 0, 21).Format(protocol.DateLayout))
	deadline := time.Now().Add(5 * time.Second)
	for {
		if ct, ok := s.Console.Task(task.ID); ok && ct.State == a2a.TaskStateInputRequired {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the console never saw the task")
		}
		time.Sleep(10 * time.Millisecond)
	}
	resp, err := http.Get(s.ConsoleURL + "/inbox")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	page, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Acme Procurement", "Approve 200 hoodies from Northwind Merch", `value="true"`} {
		if !strings.Contains(string(page), want) {
			t.Errorf("inbox page does not contain %q", want)
		}
	}
}

func statusText(task *a2a.Task) string {
	if task.Status.Message == nil {
		return ""
	}
	var texts []string
	for _, p := range task.Status.Message.Parts {
		if p.Text() != "" {
			texts = append(texts, p.Text())
		}
	}
	return strings.Join(texts, " ")
}
