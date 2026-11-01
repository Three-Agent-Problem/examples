"""A minimal A2A agent to connect to Lagrange: it answers every message with what it heard.

    pip install -r requirements.txt
    python agent.py                       # serves http://localhost:9001
    lagrange tunnel --server https://lagrange.example.com --token lgr_... --to http://localhost:9001

Replace reply() with your agent's logic.
"""

import os

import uvicorn
from starlette.applications import Starlette

from a2a.server.agent_execution.agent_executor import AgentExecutor
from a2a.server.agent_execution.context import RequestContext
from a2a.server.events.event_queue import EventQueue
from a2a.server.request_handlers import DefaultRequestHandler
from a2a.server.routes import create_agent_card_routes, create_jsonrpc_routes
from a2a.server.tasks.inmemory_task_store import InMemoryTaskStore
from a2a.server.tasks.task_updater import TaskUpdater
from a2a.types import (
    AgentCapabilities,
    AgentCard,
    AgentInterface,
    AgentSkill,
    Part,
    Task,
    TaskState,
    TaskStatus,
)

PORT = int(os.environ.get("PORT", "9001"))


def reply(text: str) -> str:
    return f"You said: {text}"


class Agent(AgentExecutor):
    async def execute(self, context: RequestContext, event_queue: EventQueue) -> None:
        await event_queue.enqueue_event(
            Task(
                id=context.task_id,
                context_id=context.context_id,
                status=TaskStatus(state=TaskState.TASK_STATE_SUBMITTED),
                history=[context.message],
            )
        )
        updater = TaskUpdater(event_queue=event_queue, task_id=context.task_id, context_id=context.context_id)
        answer = reply(context.get_user_input())
        await updater.add_artifact(parts=[Part(text=answer)], name="answer", last_chunk=True)
        await updater.complete(message=updater.new_agent_message(parts=[Part(text=answer)]))

    async def cancel(self, context: RequestContext, event_queue: EventQueue) -> None:
        await TaskUpdater(event_queue=event_queue, task_id=context.task_id, context_id=context.context_id).cancel()


card = AgentCard(
    name="Python echo",
    description="Answers every message with what it heard.",
    version="1.0.0",
    capabilities=AgentCapabilities(streaming=False),
    default_input_modes=["text"],
    default_output_modes=["text"],
    skills=[AgentSkill(id="echo", name="Echo", description="Repeats the message.", tags=["test"], examples=["Hello!"])],
    # Lagrange republishes the card at the agent's address with its own URLs, so this
    # local URL is only what the tunnel sees.
    supported_interfaces=[AgentInterface(protocol_binding="JSONRPC", protocol_version="1.0", url=f"http://localhost:{PORT}/")],
)

handler = DefaultRequestHandler(agent_executor=Agent(), task_store=InMemoryTaskStore(), agent_card=card)
app = Starlette(routes=[*create_agent_card_routes(agent_card=card), *create_jsonrpc_routes(request_handler=handler, rpc_url="/")])

if __name__ == "__main__":
    uvicorn.run(app, host="127.0.0.1", port=PORT)
