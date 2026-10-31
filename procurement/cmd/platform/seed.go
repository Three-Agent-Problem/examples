package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// A scripted request the seed sends as the logged-in user, and how it answers the
// approval question, if one comes.
type scene struct {
	text string
	// answer is the approval answer; nil leaves the question waiting in the inbox.
	answer map[string]any
}

// seed sends a fixed set of requests to @acme-buyer once the agents are online, so a dev
// server shows every kind of run: approved, auto-approved, rejected, failed, waiting on
// a person, and a plain message.
func seed(ctx context.Context, api *apiClient, handles []string) {
	if err := api.waitOnline(ctx, handles); err != nil {
		log.Printf("seed: %v", err)
		return
	}
	day := func(n int) string { return time.Now().AddDate(0, 0, n).Format("2006-01-02") }
	scenes := []scene{
		{"Order 200 hoodies, budget 6000 EUR, deliver by " + day(21), map[string]any{"approved": true, "note": "For the Q4 offsite"}},
		{"Order 100 mugs, budget 3000 EUR, deliver by " + day(21), nil},
		{"Order 300 t-shirts, budget 9000 EUR, deliver by " + day(21), map[string]any{"approved": false, "note": "Too much this quarter"}},
		{"Order 500 jackets, budget 20000 EUR, deliver by " + day(2), nil},
		{"Hi! What can you do?", nil},
		{"Order 250 hoodies, budget 8000 EUR, deliver by " + day(28), nil},
	}
	var wg sync.WaitGroup
	for _, s := range scenes {
		id, err := api.send(ctx, "acme-buyer", s.text)
		if err != nil {
			log.Printf("seed: %q: %v", s.text, err)
			continue
		}
		log.Printf("seed: sent %q (task %s)", s.text, id)
		if s.answer != nil && id != "" {
			wg.Go(func() {
				if err := api.approve(ctx, id, s.answer); err != nil {
					log.Printf("seed: answering %s: %v", id, err)
				}
			})
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(1500 * time.Millisecond):
		}
	}
	wg.Wait()
	log.Print("seed: done")
}

// waitOnline waits until every handle has a connected tunnel.
func (c *apiClient) waitOnline(ctx context.Context, handles []string) error {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		var out struct {
			Addresses []struct {
				Handle string
				Stats  struct{ Sessions int }
			}
		}
		if err := c.get(ctx, "/api/addresses", &out); err == nil {
			online := 0
			for _, a := range out.Addresses {
				for _, h := range handles {
					if a.Handle == h && a.Stats.Sessions > 0 {
						online++
					}
				}
			}
			if online == len(handles) {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
	return fmt.Errorf("agents %v did not come online", handles)
}

// send messages an address as the logged-in user; the task ID is empty for a plain message reply.
func (c *apiClient) send(ctx context.Context, to, text string) (string, error) {
	body, err := c.post(ctx, "/api/send", map[string]string{"to": to, "text": text}, http.StatusOK)
	if err != nil {
		return "", err
	}
	var out struct {
		Result struct {
			ID     string          `json:"id"`
			Status json.RawMessage `json:"status"`
		}
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	if out.Result.Status == nil {
		return "", nil
	}
	return out.Result.ID, nil
}

// approve waits for the task to ask, then answers with data.
func (c *apiClient) approve(ctx context.Context, id string, data map[string]any) error {
	for {
		var out struct{ Task struct{ State string } }
		if err := c.get(ctx, "/api/tasks/"+id, &out); err != nil {
			return err
		}
		state := strings.ToLower(out.Task.State)
		if strings.Contains(state, "input") {
			break
		}
		if strings.Contains(state, "completed") || strings.Contains(state, "failed") || strings.Contains(state, "rejected") || strings.Contains(state, "canceled") {
			return fmt.Errorf("ended %s without asking", out.Task.State)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
	// A moment in the inbox, so the live graph shows the question before the answer.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(2 * time.Second):
	}
	_, err := c.post(ctx, "/api/tasks/"+id+"/answer", map[string]any{"data": data}, http.StatusAccepted)
	return err
}

func (c *apiClient) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.server+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return statusError{resp.StatusCode, path}
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
