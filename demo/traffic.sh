#!/usr/bin/env bash
# Sends example work through every agent, so the dashboard has something to show.
# Each step says what it shows and where to look. Run ./up.sh first; run this as often as you like.
set -uo pipefail
cd "$(dirname "$0")"
ST=$(pwd)/.state
# shellcheck disable=SC1091
. "$ST/env"
N8N_PORT=${N8N_PORT:-5678}
step() { printf '\n\033[1m── %s\033[0m\n   %s\n' "$1" "$2"; }

step "1. Built-in test agent" "Runs: Ada → @echo, completed."
lagrange send @echo "Hello from the demo" --wait

step "2. A regular A2A agent (Python, via tunnel --to)" "Runs: Ada → @python-agent. Any SDK or framework looks like this."
lagrange send @python-agent "What's the status of order A-1?" --wait

step "3a. Script agent, small refund" "Runs: completed at once with data {status: refunded}."
lagrange send @refunds '{"order":"A-16","amount":40}' --wait

step "3b. Script agent, big refund" "Inbox: \"Refund 250 for order A-17?\" with Approve/Reject. Waiting: blocked on @ada. Left for you to answer."
lagrange send @refunds '{"order":"A-17","amount":250}'

step "3c. Script agent, refund rejected" "Runs: asked, Ada said no, the agent declined."
id=$(lagrange send @refunds '{"order":"A-18","amount":900}' 2>/dev/null)
sleep 2 && lagrange answer "$id" --reject && sleep 2 && lagrange task "$id"

step "3d. Script agent, bad input" "Runs: FAILED with the script's own error and a Send again button."
lagrange send @refunds "please refund something" --wait

step "4. n8n workflow as an agent (tunnel --http)" "Runs: Ada → @n8n-orders. n8n: workflow 1 has an execution."
lagrange send @n8n-orders "20 mugs" --wait
lagrange send @n8n-orders "300 hoodies, size L"
echo "   (the 300 hoodies question is in your Inbox; answering it runs the n8n workflow again)"

step "5. n8n calling an agent (n8n → Lagrange API)" "n8n workflow 3 posts to /api/send. Runs: a refund for order N-7."
curl -s "http://localhost:$N8N_PORT/webhook/ask-refunds?order=N-7&amount=60" | head -c 200; echo

step "6. An address with no agent online" "Live and Agents: @warehouse has 1 queued. Start an agent for it and the task is delivered."
lagrange send @warehouse "Reserve 50 boxes for order A-17"

if [ -f "$ST/pids/tunnel-haiku.pid" ] && kill -0 "$(cat "$ST/pids/tunnel-haiku.pid")" 2>/dev/null; then
  step "7. A headless LLM CLI (claude -p via tunnel --exec)" "Runs: Ada → @haiku."
  lagrange send @haiku "a tunnel that carries messages between agents" --wait --timeout 2m

  step "8. Claude Code as an agent (tunnel --acp)" "Runs: Ada → @claude-code, streaming its answer. It edits demo/.state/repo."
  git -C "$ST/repo" checkout -q -- . 2>/dev/null
  lagrange send @claude-code "The test in test_add.py fails. Fix add.py so it passes. Check with python3 -c 'from test_add import test_add; test_add()'. Keep the answer short." --wait --timeout 5m

  step "9. Claude using Lagrange as an MCP server" "Runs: a task to @python-agent sent by Claude through the send_task tool."
  cfg=$(printf '{"mcpServers":{"lagrange":{"command":"%s","args":["mcp"],"env":{"LAGRANGE_SERVER":"%s","LAGRANGE_KEY":"%s"}}}}' \
    "$ST/bin/lagrange" "$LAGRANGE_SERVER" "$LAGRANGE_KEY")
  (cd "$ST/empty" && echo "Use the lagrange tools: list the agents, send @python-agent the message 'Inventory check from Claude via MCP' and wait for the answer, then tell me what is waiting on people. Be brief." |
    claude -p --model haiku --mcp-config "$cfg" --strict-mcp-config \
      --allowedTools mcp__lagrange__list_agents,mcp__lagrange__send_task,mcp__lagrange__list_waiting)
fi

printf '\n\033[1mDone.\033[0m Open %s: Live, Inbox (answer the waiting ones), Waiting, Runs.\n' "$LAGRANGE_SERVER"
