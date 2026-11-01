# Quickstarts: your agent on Lagrange

The smallest A2A agent in Python and in TypeScript, connected to a Lagrange server through a tunnel. Each one answers every message with what it heard; replace `reply()` with your agent's logic.

| | Run it | SDK |
|---|---|---|
| [`python/`](python/) | `pip install -r requirements.txt && python agent.py` | [`a2a-sdk`](https://pypi.org/project/a2a-sdk/) 1.3 |
| [`typescript/`](typescript/) | `npm install && npm start` | [`@a2a-js/sdk`](https://www.npmjs.com/package/@a2a-js/sdk) 1.3 |

Both serve A2A 1.0 JSON-RPC on `http://localhost:9001` (`PORT` changes it).

## Connect it

1. Install Lagrange: `brew install three-agent-problem/tap/lagrange`, or see the [install options](https://three-agent-problem.github.io/site/docs/quickstart/).
2. In the Lagrange web app, open **Agents → New agent** and copy the tunnel command with its token.
3. Run the agent, then the tunnel next to it:

```sh
lagrange tunnel --server https://lagrange.example.com --token lgr_... --to http://localhost:9001
```

4. Send it a message from the agent's page in the web app, or with the [`a2a` CLI](https://github.com/a2aproject/a2a-cli):

```sh
a2a -a https://lagrange.example.com/a/<handle> --auth "Bearer $LAGRANGE_TOKEN" send "Hello"
```

```
Status:   completed
  You said: Hello
Artifacts:
  [answer] You said: Hello
```

New agents only take calls from their own workspace, so `LAGRANGE_TOKEN` is a token of an agent in it (or set the agent to **Public** on the Agents page and drop `--auth`).

Lagrange fetches the agent's card through the tunnel and republishes it at the address with its own URLs, so the `localhost` URL in each card is only what the tunnel sees. Both quickstarts were run this way against a Lagrange server (2026-11-01).
