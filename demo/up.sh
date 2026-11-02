#!/usr/bin/env bash
# Starts a local Lagrange with real agents of every kind, and n8n wired both ways.
# Everything stays running until ./down.sh. State lives in demo/.state (safe to delete).
#
# Needs: go, Homebrew postgresql@16, python3.12, Node 24 (for n8n; nvm is fine).
# Optional: claude (Claude Code, signed in) for @haiku and @claude-code.
set -euo pipefail
cd "$(dirname "$0")"
DEMO=$(pwd)
ST=$DEMO/.state
mkdir -p "$ST/logs" "$ST/pids" "$ST/bin"

PORT=${PORT:-8095}
PGPORT=${PGPORT:-54329}
N8N_PORT=${N8N_PORT:-5678}
SERVER=http://localhost:$PORT
# Demo login for the web app (local test account, only in this database).
EMAIL=ada@demo.test
PASS=demo-ada-pass
PG_BIN=${PG_BIN:-$(brew --prefix postgresql@16)/bin}
NODE24=${NODE24:-$(ls -d "$HOME"/.nvm/versions/node/v24*/bin 2>/dev/null | tail -1)}
PY=${PY:-$(command -v python3.12 || command -v python3)}
DB="postgres://postgres@localhost:$PGPORT/lagrange?sslmode=disable"

say() { printf '\033[1m==> %s\033[0m\n' "$*"; }
running() { [ -f "$ST/pids/$1.pid" ] && kill -0 "$(cat "$ST/pids/$1.pid")" 2>/dev/null; }
start() { # start <name> <cmd...>: run in the background unless already running
  local name=$1; shift
  if running "$name"; then return; fi
  nohup "$@" >"$ST/logs/$name.log" 2>&1 &
  echo $! >"$ST/pids/$name.pid"
}
wait_for() { # wait_for <what> <cmd...>
  local what=$1; shift
  for _ in $(seq 1 60); do "$@" >/dev/null 2>&1 && return; sleep 2; done
  echo "timed out waiting for $what (see $ST/logs)"; exit 1
}
LG=$ST/bin/lagrange

say "Building lagrange"
(cd ../../lagrange && go build -o "$LG" ./cmd/lagrange)

say "Postgres on :$PGPORT"
if [ ! -d "$ST/pg" ]; then "$PG_BIN/initdb" -D "$ST/pg" -U postgres -A trust >/dev/null; fi
if ! "$PG_BIN/pg_ctl" -D "$ST/pg" status >/dev/null 2>&1; then
  "$PG_BIN/pg_ctl" -D "$ST/pg" -o "-p $PGPORT -k ''" -l "$ST/logs/postgres.log" start >/dev/null
  sleep 2
fi
for d in lagrange n8n; do "$PG_BIN/createdb" -h localhost -p "$PGPORT" -U postgres "$d" 2>/dev/null || true; done

say "Lagrange on $SERVER"
start lagrange env DATABASE_URL="$DB" LAGRANGE_ADDR=":$PORT" LAGRANGE_PUBLIC_URL="$SERVER" \
  LAGRANGE_ADMIN_EMAIL="$EMAIL" LAGRANGE_ADMIN_PASSWORD="$PASS" LAGRANGE_ADMIN_NAME="Ada Demo" LAGRANGE_ADMIN_HANDLE=ada \
  LAGRANGE_ALLOW_PRIVATE_PUSH=1 "$LG" serve
wait_for lagrange curl -sf "$SERVER/healthz"

# An API key for Ada: the CLI, MCP and n8n use it. Saved in .state/env (source it to use lagrange yourself).
if [ ! -f "$ST/env" ]; then
  curl -sf -c "$ST/cookies" -X POST "$SERVER/api/login" -H 'Content-Type: application/json' \
    -d "{\"email\":\"$EMAIL\",\"password\":\"$PASS\"}" >/dev/null
  KEY=$(curl -sf -b "$ST/cookies" -X POST "$SERVER/api/keys" -H "Origin: $SERVER" -H 'Content-Type: application/json' \
    -d '{"name":"demo","workspace":"default","role":"admin"}' | sed -n 's/.*"secret":"\([^"]*\)".*/\1/p')
  printf 'export LAGRANGE_SERVER=%s\nexport LAGRANGE_KEY=%s\nexport PATH=%s:$PATH\n' "$SERVER" "$KEY" "$ST/bin" >"$ST/env"
fi
# shellcheck disable=SC1091
. "$ST/env"
token() { DATABASE_URL="$DB" "$LG" token --address "$1" --create --label demo; }
tunnel() { # tunnel <handle> <mode flags...>
  local h=$1; shift
  running "tunnel-$h" || start "tunnel-$h" "$LG" tunnel --server "$SERVER" --token "$(token "$h")" "$@"
}

say "Agents"
# 1. The built-in test agent.
tunnel echo --echo --name "Echo"

# 2. A regular A2A agent in Python (any SDK or framework looks the same to Lagrange).
if [ ! -d "$ST/venv" ]; then "$PY" -m venv "$ST/venv" && "$ST/venv/bin/pip" -q install -r ../quickstart/python/requirements.txt; fi
start python-agent env PORT=9001 "$ST/venv/bin/python" ../quickstart/python/agent.py
tunnel python-agent --to http://localhost:9001

# 3. A shell script: refunds up to 100 at once, asks the sender above that.
tunnel refunds --exec "$DEMO/../scenarios/script-agent/approve-refund.sh" --name "Refunds" \
  --description "Refunds orders. Up to 100 at once; asks first above that."

# 4. A headless LLM CLI and 5. a coding agent, if Claude Code is installed.
if command -v claude >/dev/null; then
  mkdir -p "$ST/empty"
  (cd "$ST/empty" && tunnel haiku --exec "claude -p --model haiku --tools '' --system-prompt 'You write one haiku about the topic you are given. Reply with the haiku only.'" \
    --name "Haiku writer" --description "Writes a haiku about anything you send.")
  if [ ! -x "$ST/acp/node_modules/.bin/claude-agent-acp" ]; then
    mkdir -p "$ST/acp" && (cd "$ST/acp" && npm install -s --no-audit --no-fund @agentclientprotocol/claude-agent-acp@0.99.1)
  fi
  if [ ! -d "$ST/repo" ]; then
    mkdir -p "$ST/repo" && (cd "$ST/repo" && git init -q &&
      printf 'def add(a, b):\n    return a - b\n' >add.py &&
      printf 'from add import add\n\ndef test_add():\n    assert add(2, 3) == 5\n' >test_add.py &&
      git add -A && git -c user.name=demo -c user.email=demo@example.com commit -qm "add() with a bug")
  fi
  PATH="$ST/acp/node_modules/.bin:$PATH" tunnel claude-code --acp claude-agent-acp --dir "$ST/repo" --name "Claude Code"
else
  echo "   (no claude on PATH: skipping @haiku and @claude-code)"
fi

# 6. An address with no agent behind it yet: tasks wait in its mailbox.
token warehouse >/dev/null

say "n8n on http://localhost:$N8N_PORT"
if [ -z "$NODE24" ]; then
  echo "   (no Node 24 found: skipping n8n; set NODE24=/path/to/node24/bin)"
else
  export PATH="$NODE24:$PATH" N8N_USER_FOLDER="$ST/n8n/home" DB_TYPE=postgresdb DB_POSTGRESDB_HOST=localhost \
    DB_POSTGRESDB_PORT="$PGPORT" DB_POSTGRESDB_DATABASE=n8n DB_POSTGRESDB_USER=postgres N8N_PORT="$N8N_PORT" \
    N8N_DIAGNOSTICS_ENABLED=false N8N_SECURE_COOKIE=false
  N8N=$ST/n8n/node_modules/.bin/n8n
  if [ ! -x "$N8N" ]; then
    echo "   installing n8n (a few minutes, once)"
    mkdir -p "$ST/n8n" && (cd "$ST/n8n" && npm install -s --no-audit --no-fund --ignore-scripts n8n@2.45.7)
  fi
  if ! running n8n; then
    for wf in order-desk lagrange-events ask-an-agent; do
      sed -e "s|__LAGRANGE__|$SERVER|g" -e "s|__KEY__|$LAGRANGE_KEY|g" "n8n/$wf.json" >"$ST/$wf.json"
      "$N8N" import:workflow --input="$ST/$wf.json" >/dev/null 2>&1
    done
    for id in orderdesk0000001 lgevents0000001 askagent0000001; do "$N8N" publish:workflow --id=$id >/dev/null 2>&1; done
    start n8n "$N8N" start
  fi
  wait_for n8n grep -q 'Finished building workflow dependency index' "$ST/logs/n8n.log"
  tunnel n8n-orders --http "http://localhost:$N8N_PORT/webhook/orders" --name "Order desk (n8n)" \
    --description "An n8n workflow: places orders, asks before orders over 100 units."
  # Lagrange → n8n: every waiting question and finished run goes to workflow 2.
  if ! curl -sf -H "Authorization: Bearer $LAGRANGE_KEY" "$SERVER/api/webhooks" | grep -q lagrange-events; then
    curl -sf -X POST "$SERVER/api/webhooks" -H "Authorization: Bearer $LAGRANGE_KEY" -H 'Content-Type: application/json' \
      -d "{\"url\":\"http://localhost:$N8N_PORT/webhook/lagrange-events\",\"events\":[\"task.waiting\",\"task.answered\",\"run.completed\",\"run.failed\"],\"description\":\"n8n workflow 2\"}" >/dev/null
  fi
fi

sleep 3
cat <<EOF

$(say "Running")
  Lagrange web app   $SERVER        log in: $EMAIL / see demo/README.md
  n8n                http://localhost:$N8N_PORT   (first visit: create a local owner account)
  CLI                source $ST/env && lagrange runs

  Next: ./traffic.sh sends example work through every agent. ./down.sh stops everything.
EOF
