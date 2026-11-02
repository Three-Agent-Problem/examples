# An n8n workflow as an agent, and n8n reacting to Lagrange

Two directions, two workflows:

```
any A2A caller ──▶ Lagrange @n8n-orders ──tunnel --http──▶ n8n "Order desk" webhook ──▶ reply / ask
Lagrange webhooks (task.waiting, run.completed) ──▶ n8n "Lagrange events" ──▶ your service (Slack, CRM, …)
```

## 1. The workflow is the agent: `order-desk.json`

The workflow is a Webhook (`POST /webhook/orders`, answering with *Respond to Webhook*) and one Respond node. It places small orders, asks before orders over 100 units, and handles the answer:

| In (POSTed by the tunnel) | Out |
|---|---|
| `{"text": "20 mugs"}` | `{"output": "Order placed: 20 mugs", "status": "ordered", "quantity": 20}` |
| `{"text": "300 hoodies"}` | `{"ask": "Approve an order of 300 units?", "schema": {approved: boolean}}` |
| `{"answer": true, "data": {"approved": true}}` | `{"output": "Approved: the order is placed."}` |

Swap the Respond node's expression for an AI Agent node and reply with its `{"output": …}`, which works as is.

```sh
# import the workflow into n8n and activate it
lagrange tunnel --address n8n-orders --create --http https://n8n.example.com/webhook/orders \
  --name "Order desk" --description "Places orders and asks before big ones."

lagrange send @n8n-orders "20 mugs" --wait          # Order placed: 20 mugs
lagrange send @n8n-orders "300 hoodies, size L"     # waits: the question is in your inbox, email or Slack
lagrange answer <task> --approve                    # Approved: the order is placed.
```

To protect the webhook, give it Header Auth in n8n and pass the header with `--header "X-Key: …"`.

## 2. n8n reacts to Lagrange: `on-waiting.json`

In Lagrange, go to **Settings → Webhooks** (or `POST /api/webhooks`) and point the `task.waiting` and `run.completed` events at `https://n8n.example.com/webhook/lagrange-events`. The workflow forwards each event to `http://127.0.0.1:9078/notify`. Replace that node with Slack, a CRM, a ticket, or anything else n8n can reach. Each event carries the plain-language question, who it waits on, and a link to the task:

```json
{"type": "task.waiting", "data": {"state": "input-required", "question": "Approve an order of 150 units? (150 tote bags)", "from": "ada", "to": "n8n-orders", ...}}
{"type": "run.completed", "data": {"state": "completed", "runId": "...", "url": "https://lagrange.example.com/tasks/..."}}
```

Deliveries are signed (`Lagrange-Signature`), and their secret is shown once when the webhook is created. Lagrange refuses to deliver to private addresses (`localhost`, LAN) unless the server runs with `LAGRANGE_ALLOW_PRIVATE_PUSH=1`. Set it when n8n sits next to a self-hosted Lagrange.

## What we ran (2026-11-02)

- n8n 2.45.7, from npm on Node 24, with Postgres as its database. The default SQLite needs a native build.
- Lagrange on :8095, with `lagrange send` / `answer` as Ada.
- Every row in the tables above came from a real run. The catcher received `task.waiting` with the question, then `run.completed`.

Gotchas we hit:
- n8n blocks `$env` in expressions (`N8N_BLOCK_ENV_ACCESS_IN_NODE`), so URLs are written into the workflow.
- Workflows imported with the CLI need `n8n publish:workflow --id …` and an n8n restart before their webhooks are live.
