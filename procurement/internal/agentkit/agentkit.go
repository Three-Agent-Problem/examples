// Package agentkit wires an a2a-go executor into an HTTP server the same way for every
// agent in this example: JSON-RPC on /invoke, the card on the well-known path, push
// notifications enabled, and, when a console is configured, an observer that copies
// every task event to it.
package agentkit

import (
	"net/http"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/a2aproject/a2a-go/v2/a2asrv/push"
	"github.com/three-agent-problem/lagrange/observer"
)

// InvokePath is where the JSON-RPC endpoint is mounted.
const InvokePath = "/invoke"

// Observe points an agent's task events at a console. A zero value disables it.
type Observe struct {
	PushURL string
	Token   string
}

// Card builds an agent card with a JSON-RPC interface at baseURL + InvokePath.
func Card(name, description, baseURL string, caps a2a.AgentCapabilities, skills ...a2a.AgentSkill) *a2a.AgentCard {
	return &a2a.AgentCard{
		Name:        name,
		Description: description,
		Version:     "0.1.0",
		Provider:    &a2a.AgentProvider{Org: name, URL: baseURL},
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(strings.TrimSuffix(baseURL, "/")+InvokePath, a2a.TransportProtocolJSONRPC),
		},
		DefaultInputModes:  []string{"text/plain", "application/json"},
		DefaultOutputModes: []string{"text/plain", "application/json"},
		Capabilities:       caps,
		Skills:             skills,
	}
}

// Mux returns a mux serving the agent. Callers may add more routes to it.
// opts are added to the request handler's options, for example
// a2asrv.WithTaskStore(store) with a FileStore so tasks survive a restart.
func Mux(card *a2a.AgentCard, exec a2asrv.AgentExecutor, obs Observe, opts ...a2asrv.RequestHandlerOption) *http.ServeMux {
	var store push.ConfigStore = push.NewInMemoryStore()
	if obs.PushURL != "" {
		store = observer.Wrap(store, obs.PushURL, obs.Token)
	}
	// Every agent runs on localhost in this example, so the push SSRF guard (spec 13.2)
	// has to allow private networks. A deployed agent must keep it on.
	sender := push.NewHTTPPushSender(&push.HTTPSenderConfig{Timeout: 5 * time.Second, AllowPrivateNetworks: true})
	handler := a2asrv.NewHandler(exec, append([]a2asrv.RequestHandlerOption{a2asrv.WithPushNotifications(store, sender)}, opts...)...)

	mux := http.NewServeMux()
	mux.Handle(InvokePath, a2asrv.NewJSONRPCHandler(handler))
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))
	return mux
}
