#!/bin/sh
# Claude Code as a Lagrange address: a scratch repo with a failing test, a tunnel, a task.
# Needs: lagrange (logged in, or LAGRANGE_SERVER + LAGRANGE_KEY), claude-agent-acp, Claude Code signed in.
set -eu

HANDLE=${HANDLE:-ada-claude}
REPO=$(mktemp -d)
cd "$REPO"
git init -q
printf 'def add(a, b):\n    return a - b\n' > add.py
printf 'from add import add\n\ndef test_add():\n    assert add(2, 3) == 5\n' > test_add.py
git add -A && git -c user.name=demo -c user.email=demo@example.com commit -qm "add() with a bug"
echo "scratch repo: $REPO"

lagrange tunnel --address "$HANDLE" --create --acp claude-agent-acp --dir "$REPO" --name "Claude Code" &
TUNNEL=$!
trap 'kill $TUNNEL 2>/dev/null' EXIT
sleep 5

lagrange send "@$HANDLE" "The test in test_add.py fails. Fix add.py so it passes. Check with python3 -c 'from test_add import test_add; test_add()'. Keep the answer short." --wait --timeout 5m
git -C "$REPO" diff
