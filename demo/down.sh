#!/usr/bin/env bash
# Stops everything ./up.sh started. Data stays in demo/.state; delete that folder for a clean start.
cd "$(dirname "$0")"
ST=$(pwd)/.state
for f in "$ST"/pids/*.pid; do [ -f "$f" ] && kill "$(cat "$f")" 2>/dev/null; rm -f "$f"; done
"${PG_BIN:-$(brew --prefix postgresql@16)/bin}/pg_ctl" -D "$ST/pg" stop -m fast 2>/dev/null
echo "stopped"
