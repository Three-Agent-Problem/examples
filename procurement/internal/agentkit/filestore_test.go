package agentkit

import (
	"context"
	"iter"
	"net/http/httptest"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
)

// asker asks a question on the first message and completes on the answer, reading the
// question back from the stored task, as the buyer reads its quote comparison.
var asker = a2asrv.AgentExecutorFunc(func(_ context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		if ec.StoredTask == nil {
			if !yield(a2a.NewSubmittedTask(ec, ec.Message), nil) {
				return
			}
			q := a2a.NewMessageForTask(a2a.MessageRoleAgent, ec, a2a.NewTextPart("Approve 5,700 EUR?"))
			yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateInputRequired, q), nil)
			return
		}
		asked := ec.StoredTask.Status.Message.Parts[0].Text()
		done := a2a.NewMessageForTask(a2a.MessageRoleAgent, ec, a2a.NewTextPart(asked+" "+ec.Message.Parts[0].Text()))
		yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCompleted, done), nil)
	}
})

// serve runs the agent with a FileStore in dir and returns a client and a stop func.
func serve(t *testing.T, dir string) (*a2aclient.Client, func()) {
	t.Helper()
	store, err := NewFileStore(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(Mux(Card("Asker", "asks", "http://agent", a2a.AgentCapabilities{}), asker, Observe{}, a2asrv.WithTaskStore(store)))
	c, err := a2aclient.NewFromEndpoints(t.Context(), []*a2a.AgentInterface{a2a.NewAgentInterface(srv.URL+InvokePath, a2a.TransportProtocolJSONRPC)})
	if err != nil {
		t.Fatal(err)
	}
	return c, srv.Close
}

// A question the agent asked before it restarted can still be answered after.
func TestFileStoreSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	c, stop := serve(t, dir)
	res, err := c.SendMessage(t.Context(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("Order hoodies"))})
	if err != nil {
		t.Fatal(err)
	}
	task, ok := res.(*a2a.Task)
	if !ok || task.Status.State != a2a.TaskStateInputRequired {
		t.Fatalf("first answer = %#v", res)
	}
	stop()

	c, stop = serve(t, dir)
	defer stop()
	answer := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("yes"))
	answer.TaskID, answer.ContextID = task.ID, task.ContextID
	res, err = c.SendMessage(t.Context(), &a2a.SendMessageRequest{Message: answer})
	if err != nil {
		t.Fatal(err)
	}
	done, ok := res.(*a2a.Task)
	if !ok || done.Status.State != a2a.TaskStateCompleted || done.Status.Message.Parts[0].Text() != "Approve 5,700 EUR? yes" {
		t.Fatalf("after restart = %#v", res)
	}
	got, err := c.GetTask(t.Context(), &a2a.GetTaskRequest{ID: task.ID})
	if err != nil || len(got.History) < 2 {
		t.Fatalf("stored task = %+v, %v", got, err)
	}
}
