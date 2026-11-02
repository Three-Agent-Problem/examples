# Demo: every kind of agent on one local Lagrange

One command starts a Lagrange server with real agents of every kind behind it, plus n8n wired in both directions. A second command sends example work through all of them, so the dashboard has something to show. Everything stays running until you stop it.

```sh
./up.sh        # start everything (safe to run again)
./traffic.sh   # send a round of example work through every agent
./down.sh      # stop everything; data stays in .state/ (delete it for a clean start)
```

| What | Where |
|---|---|
| Lagrange dashboard | http://localhost:8095. Log in as `ada@demo.test` / `demo-ada-pass` (a local test account in the demo database). |
| n8n | http://localhost:5678. Create a local owner account on the first visit. |
| The CLI as Ada | `source .state/env && lagrange runs` |

**Needs:** go, Homebrew `postgresql@16`, python3.12 and Node 24 (for n8n; nvm is fine). Claude Code (`claude`, signed in) is optional: without it, `@haiku` and `@claude-code` are skipped. The first run installs n8n (about 2.5 GB, a few minutes).

## The agents

| Address | What it is | How it's connected |
|---|---|---|
| `@echo` | Built-in test agent | `tunnel --echo` |
| `@python-agent` | A regular A2A agent (`../quickstart/python`) | `tunnel --to http://localhost:9001` |
| `@refunds` | A shell script that refunds up to 100 at once and asks above that (`../scenarios/script-agent/approve-refund.sh`) | `tunnel --exec` |
| `@haiku` | `claude -p` with no tools and a fixed role | `tunnel --exec` |
| `@claude-code` | Claude Code working in `.state/repo` | `tunnel --acp claude-agent-acp` |
| `@n8n-orders` | n8n workflow 1: places orders, asks before more than 100 units | `tunnel --http` |
| `@warehouse` | An address with no agent yet; its tasks queue up | (none) |

n8n workflows (`n8n/`):
1. **Order desk:** the workflow *is* an agent.
2. **Lagrange events:** receives Lagrange's webhooks (`task.waiting`, `task.answered`, `run.completed`, `run.failed`).
3. **Ask an agent:** n8n calls `@refunds` through `POST /api/send`. Open `http://localhost:5678/webhook/ask-refunds?order=Z-1&amount=500`.

## What `traffic.sh` sends

1. `@echo`: hello. Completed.
2. `@python-agent`: a question. Completed.
3. `@refunds`:
   1. A refund of 40: done at once.
   2. A refund of 250: waits in your Inbox.
   3. A refund of 900: rejected.
   4. Bad input: failed.
4. `@n8n-orders`:
   - 20 mugs: placed.
   - 300 hoodies: waits in your Inbox.
5. n8n workflow 3 sends a refund of 60 to `@refunds`.
6. `@warehouse`: queued, because no agent is connected.
7. `@haiku`: a haiku.
8. `@claude-code` fixes the failing test in `.state/repo`.
9. Claude, through `lagrange mcp`, lists the agents, sends `@python-agent` a task, and reads what's waiting.

## Changing the ports

`PORT` (Lagrange, 8095), `PGPORT` (Postgres, 54329) and `N8N_PORT` (5678) are environment variables for `up.sh`.
