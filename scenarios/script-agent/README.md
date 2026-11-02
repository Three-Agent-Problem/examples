# Any script or CLI agent as an address: `--exec`

`lagrange tunnel --exec "<cmd>"` runs the command once per message. The message text goes to stdin and stdout is the answer. You write no A2A code.

## A headless LLM CLI as an agent

```sh
lagrange tunnel --address haiku --create \
  --exec "claude -p --model haiku --tools '' --system-prompt 'You write one haiku about the topic you are given. Reply with the haiku only.'" \
  --name "Haiku writer" --description "Writes a haiku about anything you send."

lagrange send @haiku "a tunnel that carries messages between agents" --wait
```

```
Quiet conduit runs,
sealed packets hum through the dark,
each word finds its door.
completed (task 01a19afc-fd2a-77a5-bfd4-e085ebb33e5a)
```

The same works with `llm`, `ollama run llama3`, `gemini -p` or your own `python agent.py`.

> **Lock the tools down.** A coding agent run with `-p` uses your machine's permission settings. Our first try left out `--tools ''` and the system prompt, and Claude read "a tunnel that carries messages between agents" as a job: it wrote and tested a relay server in its working directory. Give a wrapped agent a fixed role and only the tools it needs, and run it from an empty directory. For a coding agent that should ask before it acts, use `--acp` instead (see [`../claude-code-teammate`](../claude-code-teammate/)).

## A script that asks before it acts

`approve-refund.sh` refunds small amounts right away and asks the sender about larger ones. When the sender answers, the script runs again with `LAGRANGE_ANSWER=1`.

```sh
lagrange tunnel --address refunds --create --exec ./approve-refund.sh --name Refunds
lagrange send @refunds '{"order":"A-17","amount":250}' --wait   # the sender gets an Approve/Reject form
```

## The contract

| | |
|---|---|
| stdin | the message text |
| `LAGRANGE_DATA` | the first JSON part, if any |
| `LAGRANGE_TASK_ID`, `LAGRANGE_CONTEXT_ID` | for your own state between calls |
| `LAGRANGE_ANSWER=1` | this call answers your question |
| stdout | plain text, or JSON: `{"output": …}` or `{"ask": "…", "schema": {…}}` |
| exit ≠ 0 | the task fails with the end of stderr |
| `--timeout` | default 5m |
