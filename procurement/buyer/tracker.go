package buyer

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// tracker remembers the last state of each task a supplier pushed to the buyer's
// webhook, and wakes whoever waits for that task.
//
// The first push can arrive before SendMessage has even returned the task ID, so state
// is recorded for any task ID and a waiter checks it before blocking.
type tracker struct {
	mu      sync.Mutex
	states  map[a2a.TaskID]a2a.TaskState
	changed map[a2a.TaskID]chan struct{}
}

func newTracker() *tracker {
	return &tracker{states: make(map[a2a.TaskID]a2a.TaskState), changed: make(map[a2a.TaskID]chan struct{})}
}

func (t *tracker) update(id a2a.TaskID, state a2a.TaskState) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.states[id] = state
	if ch, ok := t.changed[id]; ok {
		close(ch)
		delete(t.changed, id)
	}
}

// wait blocks until the task is terminal or interrupted and returns that state.
func (t *tracker) wait(ctx context.Context, id a2a.TaskID) (a2a.TaskState, error) {
	for {
		t.mu.Lock()
		state := t.states[id]
		if state.Terminal() || state == a2a.TaskStateInputRequired || state == a2a.TaskStateAuthRequired {
			t.mu.Unlock()
			return state, nil
		}
		ch, ok := t.changed[id]
		if !ok {
			ch = make(chan struct{})
			t.changed[id] = ch
		}
		t.mu.Unlock()

		select {
		case <-ctx.Done():
			return state, ctx.Err()
		case <-ch:
		}
	}
}

// handler is the webhook. a2a-go's push sender posts a StreamResponse and sends the
// push config's token in the A2A-Notification-Token header.
func (t *tracker) handler(token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("A2A-Notification-Token") != token {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}
		var sr a2a.StreamResponse
		if err := json.NewDecoder(r.Body).Decode(&sr); err != nil {
			http.Error(w, "body is not an A2A StreamResponse", http.StatusBadRequest)
			return
		}
		switch ev := sr.Event.(type) {
		case *a2a.Task:
			t.update(ev.ID, ev.Status.State)
		case *a2a.TaskStatusUpdateEvent:
			t.update(ev.TaskID, ev.Status.State)
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
