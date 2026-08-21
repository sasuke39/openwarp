#!/usr/bin/env node
import { createRequire } from 'node:module'
import { createServer, type Socket } from 'node:net'
import { mkdir, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createInterface } from 'node:readline'
import { DeepSeekHarness, type HarnessNotification } from '@deepseek-ai/dsh-sdk-client'
import { envelope, parseEnvelope, type ExternalToolCall, type TurnRequest } from './protocol.js'

assertSupportedNode()

interface SessionState {
  harness: DeepSeekHarness
  exchangeId: string
  taskId: string
  running: boolean
  sawTextDelta: boolean
}

const require = createRequire(import.meta.url)
const runtimeBin = require.resolve('@deepseek-ai/dsh-sdk-jsonrpc-demo/bin')
const integrationRoot = dirname(dirname(fileURLToPath(import.meta.url)))
const configPath = join(integrationRoot, 'cordis.yml')
const socketPath = join(tmpdir(), `open-warp-dsh-${process.pid}.sock`)
const sessionRoot = process.env.DSH_SESSION_ROOT ?? join(tmpdir(), 'open-warp-dsh-sessions')
const sessions = new Map<string, SessionState>()
const callOwners = new Map<string, Socket>()
let shutdownPromise: Promise<void> | undefined

await mkdir(sessionRoot, { recursive: true })
await rm(socketPath, { force: true })

const toolServer = createServer(socket => attachToolSocket(socket))
await new Promise<void>((resolve, reject) => {
  toolServer.once('error', reject)
  toolServer.listen(socketPath, () => {
    toolServer.off('error', reject)
    resolve()
  })
})

const lines = createInterface({ input: process.stdin, crlfDelay: Infinity })
lines.on('line', line => {
  if (line.trim().length === 0) return
  void handleFrame(line).catch(error => writeEvent('runtime', {
    type: 'turn.failed',
    error: error instanceof Error ? error.message : String(error),
  }))
})
lines.on('close', () => void shutdown())

async function handleFrame(line: string): Promise<void> {
  const frame = parseEnvelope(line)
  switch (frame.type) {
    case 'turn.start':
      startTurn(frame.exchange_id, frame.payload as TurnRequest)
      return
    case 'turn.resume':
      resumeTurn(frame.exchange_id, frame.payload as TurnRequest)
      return
    case 'turn.cancel': {
      const taskId = (frame.payload as { task_id?: unknown })?.task_id
      if (typeof taskId !== 'string') throw new Error('turn.cancel requires task_id')
      await cancelTask(taskId)
      return
    }
    case 'runtime.shutdown':
      await shutdown()
      return
    default:
      throw new Error(`unsupported frame type ${frame.type}`)
  }
}

function startTurn(exchangeId: string, request: TurnRequest): void {
  const prompt = request.inputs
    .filter(input => input.kind === 'user.message')
    .map(input => input.content)
    .join('\n\n')
  if (prompt.length === 0) throw new Error('turn.start requires a user message')

  let state = sessions.get(request.conversation_id)
  if (state === undefined) {
    const runtimeEnv = {
      ...process.env,
      DSH_WARP_BRIDGE_SOCKET: socketPath,
      DSH_SESSION_ROOT: sessionRoot,
      DSH_SYSTEM_PROMPT: request.system_prompt ?? 'You are a coding agent in Warp.',
      DSH_CWD: request.working_dir ?? process.cwd(),
    }
    state = {
      harness: new DeepSeekHarness({
        launch: {
          command: process.execPath,
          args: [runtimeBin, configPath],
          env: runtimeEnv,
        },
        cwd: process.cwd(),
        provider: process.env.DSH_PROVIDER ?? 'deepseek-official',
        model: process.env.DSH_MODEL ?? 'deepseek-v4-flash',
        ...(positiveInteger(process.env.DSH_MAX_TOKENS) === undefined
          ? {}
          : { maxTokens: positiveInteger(process.env.DSH_MAX_TOKENS) }),
      }),
      exchangeId,
      taskId: request.task_id,
      running: false,
      sawTextDelta: false,
    }
    sessions.set(request.conversation_id, state)
  }
  if (state.running) throw new Error(`conversation ${request.conversation_id} already has a running turn`)
  state.exchangeId = exchangeId
  state.taskId = request.task_id
  state.running = true
  state.sawTextDelta = false
  void state.harness.run(prompt, {
    sessionId: request.conversation_id,
    onNotification: notification => onNotification(request.conversation_id, notification),
  }).then(result => {
    const current = sessions.get(request.conversation_id)
    if (current === undefined) return
    if (!current.sawTextDelta && result.finalResponse.length > 0) {
      writeEvent(current.exchangeId, { type: 'assistant.final', text: result.finalResponse })
    }
    current.running = false
    if (!current.sawTextDelta && result.finalResponse.length === 0) {
      writeEvent(current.exchangeId, { type: 'turn.failed', error: 'DeepSeek Harness completed without an assistant response' })
      return
    }
    writeEvent(current.exchangeId, { type: 'turn.completed' })
  }).catch(error => {
    const current = sessions.get(request.conversation_id)
    if (current === undefined) return
    current.running = false
    writeEvent(current.exchangeId, {
      type: 'turn.failed',
      error: error instanceof Error ? error.message : String(error),
    })
  })
}

function resumeTurn(exchangeId: string, request: TurnRequest): void {
  const state = sessions.get(request.conversation_id)
  if (state === undefined || !state.running) throw new Error(`conversation ${request.conversation_id} has no suspended turn`)
  state.exchangeId = exchangeId
  state.taskId = request.task_id
  for (const input of request.inputs) {
    if (input.kind !== 'tool.result' || input.tool_call_id === undefined) continue
    const owner = callOwners.get(input.tool_call_id)
    if (owner === undefined) throw new Error(`unknown external tool call ${input.tool_call_id}`)
    callOwners.delete(input.tool_call_id)
    owner.write(`${JSON.stringify({ type: 'tool.result', id: input.tool_call_id, content: input.content })}\n`)
  }
}

function onNotification(conversationId: string, notification: HarnessNotification): void {
  if (notification.method !== 'session.event') return
  const state = sessions.get(conversationId)
  if (state === undefined) return
  const event = notification.params.event as { type?: string; data?: Record<string, unknown> } | undefined
  if (event?.type === 'assistant/chunk') {
    const chunk = event.data?.chunk as { type?: string; text?: unknown } | undefined
    if (chunk?.type === 'text-delta' && typeof chunk.text === 'string') {
      state.sawTextDelta = true
      writeEvent(state.exchangeId, { type: 'assistant.delta', text: chunk.text })
    }
    return
  }
  if (event?.type === 'todo/write') {
    writeEvent(state.exchangeId, { type: 'todo.changed', data: event.data })
  }
}

function attachToolSocket(socket: Socket): void {
  let buffer = ''
  socket.setEncoding('utf8')
  socket.on('data', chunk => {
    buffer += chunk
    while (true) {
      const newline = buffer.indexOf('\n')
      if (newline < 0) break
      const line = buffer.slice(0, newline)
      buffer = buffer.slice(newline + 1)
      if (line.trim().length === 0) continue
      const message = JSON.parse(line) as { type?: string; calls?: ExternalToolCall[]; id?: string }
      if (message.type === 'tool.cancel' && typeof message.id === 'string') {
        callOwners.delete(message.id)
        continue
      }
      if (message.type !== 'tool.call.batch' || !Array.isArray(message.calls) || message.calls.length === 0) continue
      const sessionId = message.calls[0]?.session_id
      const state = sessionId === undefined ? undefined : sessions.get(sessionId)
      if (state === undefined) {
        for (const call of message.calls) {
          socket.write(`${JSON.stringify({ type: 'tool.result', id: call.id, content: 'No active Warp session owns this tool call.', is_error: true })}\n`)
        }
        continue
      }
      for (const call of message.calls) callOwners.set(call.id, socket)
      writeEvent(state.exchangeId, {
        type: 'tool.call.batch',
        tool_calls: message.calls.map(call => ({ id: call.id, name: workspaceToolName(call.name), arguments: call.arguments })),
      })
      writeEvent(state.exchangeId, { type: 'turn.awaiting_tool' })
    }
  })
  socket.on('close', () => {
    for (const [id, owner] of callOwners) {
      if (owner === socket) callOwners.delete(id)
    }
  })
}

async function cancelTask(taskId: string): Promise<void> {
  const matching = [...sessions.entries()].filter(([, state]) => state.taskId === taskId)
  await Promise.all(matching.map(async ([conversationId, state]) => {
    sessions.delete(conversationId)
    await state.harness.close()
  }))
}

function writeEvent(exchangeId: string, event: Record<string, unknown>): void {
  process.stdout.write(`${JSON.stringify(envelope(exchangeId, 'event', event))}\n`)
}

async function shutdown(): Promise<void> {
	if (shutdownPromise !== undefined) return shutdownPromise
	shutdownPromise = (async () => {
		lines.close()
		await new Promise<void>(resolve => toolServer.close(() => resolve()))
		await Promise.all([...sessions.values()].map(state => state.harness.close()))
		sessions.clear()
		await rm(socketPath, { force: true })
	})()
	return shutdownPromise
}

function positiveInteger(raw: string | undefined): number | undefined {
  if (raw === undefined || raw.length === 0) return undefined
  const value = Number(raw)
  return Number.isSafeInteger(value) && value > 0 ? value : undefined
}

function workspaceToolName(dshName: string): string {
  const names: Record<string, string> = {
    bash: 'workspace.shell',
    read: 'workspace.read_file',
    write: 'workspace.write_file',
    edit: 'workspace.edit_file',
    glob: 'workspace.glob',
    grep: 'workspace.grep',
  }
  const name = names[dshName]
  if (name === undefined) throw new Error(`DSH tool ${dshName} is not a workspace proxy`)
  return name
}

function assertSupportedNode(): void {
  const [major = 0, minor = 0] = process.versions.node.split('.').map(Number)
  if (major >= 24 || (major === 22 && minor >= 19)) return
  throw new Error(`DeepSeek Harness requires Node ^22.19 or >=24; current runtime is ${process.versions.node}`)
}
