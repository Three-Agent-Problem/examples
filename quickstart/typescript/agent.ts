// A minimal A2A agent to connect to Lagrange: it answers every message with what it heard.
//
//   npm install
//   npm start                              # serves http://localhost:9001
//   lagrange tunnel --server https://lagrange.example.com --token lgr_... --to http://localhost:9001
//
// Replace reply() with your agent's logic.
import { randomUUID } from 'node:crypto'
import express from 'express'
import { A2A_PROTOCOL_VERSION, AGENT_CARD_PATH, Role, TaskState, type AgentCard, type Message, type Part, type TaskStatus } from '@a2a-js/sdk'
import { AgentEvent, DefaultRequestHandler, InMemoryTaskStore, type AgentExecutor } from '@a2a-js/sdk/server'
import { agentCardHandler, jsonRpcHandler, UserBuilder } from '@a2a-js/sdk/server/express'

const port = Number(process.env.PORT ?? 9001)

function reply(text: string): string {
  return `You said: ${text}`
}

// The SDK's types are generated from the protobuf definitions, so every field is spelled out.
const textPart = (value: string): Part => ({ content: { $case: 'text', value }, metadata: undefined, filename: '', mediaType: '' })
const status = (state: TaskState, message?: Message): TaskStatus => ({ state, message, timestamp: undefined })

const agent: AgentExecutor = {
  async execute(ctx, bus) {
    const text = ctx.userMessage.parts.map((p) => (p.content?.$case === 'text' ? p.content.value : '')).join(' ')
    const answer: Message = {
      messageId: randomUUID(),
      contextId: ctx.contextId,
      taskId: ctx.taskId,
      role: Role.ROLE_AGENT,
      parts: [textPart(reply(text))],
      metadata: undefined,
      extensions: [],
      referenceTaskIds: [],
    }
    bus.publish(AgentEvent.task({ id: ctx.taskId, contextId: ctx.contextId, status: status(TaskState.TASK_STATE_SUBMITTED), history: [ctx.userMessage], artifacts: [], metadata: undefined }))
    bus.publish(AgentEvent.artifactUpdate({ taskId: ctx.taskId, contextId: ctx.contextId, artifact: { artifactId: randomUUID(), name: 'answer', description: '', parts: answer.parts, metadata: undefined, extensions: [] }, append: false, lastChunk: true, metadata: undefined }))
    bus.publish(AgentEvent.statusUpdate({ taskId: ctx.taskId, contextId: ctx.contextId, status: status(TaskState.TASK_STATE_COMPLETED, answer), metadata: undefined }))
    bus.finished()
  },
  async cancelTask(taskId, bus) {
    bus.publish(AgentEvent.statusUpdate({ taskId, contextId: '', status: status(TaskState.TASK_STATE_CANCELED), metadata: undefined }))
    bus.finished()
  },
}

const card: AgentCard = {
  name: 'TypeScript echo',
  description: 'Answers every message with what it heard.',
  version: '1.0.0',
  provider: undefined,
  // Lagrange republishes the card at the agent's address with its own URLs, so this
  // local URL is only what the tunnel sees.
  supportedInterfaces: [{ url: `http://localhost:${port}/`, protocolBinding: 'JSONRPC', protocolVersion: A2A_PROTOCOL_VERSION, tenant: '' }],
  capabilities: { streaming: false, pushNotifications: false, extensions: [], extendedAgentCard: false },
  securitySchemes: {},
  securityRequirements: [],
  defaultInputModes: ['text'],
  defaultOutputModes: ['text'],
  skills: [{ id: 'echo', name: 'Echo', description: 'Repeats the message.', tags: ['test'], examples: ['Hello!'], inputModes: [], outputModes: [], securityRequirements: [] }],
  documentationUrl: '',
  signatures: [],
}

const handler = new DefaultRequestHandler(card, new InMemoryTaskStore(), agent)
const app = express()
app.use(`/${AGENT_CARD_PATH}`, agentCardHandler({ agentCardProvider: handler }))
app.use(jsonRpcHandler({ requestHandler: handler, userBuilder: UserBuilder.noAuthentication }))
app.listen(port, '127.0.0.1', () => console.log(`TypeScript echo on http://localhost:${port}`))
