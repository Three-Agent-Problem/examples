# examples

Runnable multi-agent applications built on [A2A](https://a2a-protocol.org), [a2a-go](https://github.com/a2aproject/a2a-go), and this project's [`lagrange`](https://github.com/three-agent-problem/lagrange) and [`registry`](https://github.com/three-agent-problem/registry).

| Example | Shows |
|---|---|
| [`quickstart`](quickstart/) | The smallest A2A agent in Python (`a2a-sdk`) and TypeScript (`@a2a-js/sdk`), connected to Lagrange with `lagrange tunnel --to`. Start here to put your own agent on Lagrange. |
| [`scenarios`](scenarios/) | Real agents through Lagrange, each run live: [Claude Code](scenarios/claude-code-teammate/) as an address (`--acp`), [scripts and CLI agents](scenarios/script-agent/) (`--exec`), and an [n8n workflow](scenarios/n8n-workflow/) as an agent (`--http`) plus n8n reacting to Lagrange webhooks. |
| [`procurement`](procurement/) | An orchestrator agent that finds suppliers in a registry, compares quotes, waits for a person to approve (`INPUT_REQUIRED` + structured input), places an order with push notifications, asks for a calendar connection (`AUTH_REQUIRED` + a credential broker, demo or [Nango](https://nango.dev)), and finishes with a purchase order. The console traces all of it across agents. The same agents also run on a Lagrange server through tunnels (`procurement/cmd/platform`). |

Planned: a mixed-language support desk (Python triage, TypeScript refunds, a person, driven from an MCP client) and a long-running research agent that reports through push.

Each example is its own Go module. Until `lagrange` and `registry` are released, the examples resolve them from sibling checkouts (`../../lagrange`, `../../registry`) through `replace` lines in their `go.mod`.

## License

Apache-2.0
