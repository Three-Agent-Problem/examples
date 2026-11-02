# Claude Code as a teammate with an address

Run Claude Code on your machine, give it a Lagrange address, and send it work from anywhere: a script, CI, another agent or the web app.

```
you / CI / another agent ──A2A──▶ Lagrange (@ada-claude) ──tunnel──▶ claude-agent-acp ──▶ your repo
```

## Run it

You need a Lagrange server, an API key (`lagrange login`, or Settings → API keys) and Claude Code signed in on this machine.

```sh
npm install -g @agentclientprotocol/claude-agent-acp     # the ACP adapter for Claude Code
./run.sh                                                 # makes a scratch repo with a failing test, connects the tunnel, sends the task
```

`run.sh` does the following:
1. Makes a scratch git repo where `add()` subtracts and its test fails.
2. Connects `lagrange tunnel --address ada-claude --create --acp claude-agent-acp --dir <repo>`.
3. Sends `lagrange send @ada-claude "The test in test_add.py fails. Fix add.py so it passes." --wait`.
4. Prints the answer and `git diff`.

## What happened when we ran it (2026-11-02)

```
$ lagrange send @ada-claude "The test in test_add.py fails. Fix add.py so it passes. …" --wait
I fixed `add.py` by changing `a - b` to `a + b`. … `test_add()` passed.
completed (task 01a19af8-3eaf-7d04-b3d6-a741fc13f5c0)

$ git diff --stat
 add.py | 2 +-
```

The run shows up in **Runs** as Ada → @ada-claude, with the streamed answer and the HTTP exchanges.

## Permission prompts

When Claude Code asks to run a tool, the question becomes `INPUT_REQUIRED` with an Approve/Reject form. It lands in the sender's inbox, with an email or Slack link if one is set up. The adapter follows your Claude Code permission settings. If those already allow edits and shell commands, as on our test machine, Claude Code doesn't ask and the task runs straight through.

## Make it a worker pool

Run the same tunnel command on more machines. Tasks to `@ada-claude` spread across them, and `--capacity` caps how many tasks each copy runs at once.
