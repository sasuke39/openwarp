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
import { workspaceToolArguments } from './workspace-tool-arguments.js'

assertSupportedNode()

interface SessionState {
  harness: DeepSeekHarness
  exchangeId: string
  turnId: string
  taskId: string
  running: boolean
  sawTextDelta: boolean
  pendingSteers: Map<string, string>
}

const require = createRequire(import.meta.url)
const runtimeBin = require.resolve('@deepseek-ai/dsh-sdk-jsonrpc-demo/bin')
const integrationRoot = dirname(dirname(fileURLToPath(import.meta.url)))
const configPath = join(integrationRoot, 'cordis.yml')
const socketPath = join(tmpdir(), `open-warp-dsh-${process.pid}.sock`)
const sessionRoot = process.env.DSH_SESSION_ROOT ?? join(tmpdir(), 'open-warp-dsh-sessions')
const sessions = new Map<string, SessionState>()
const queues = new Map<string, Promise<void>>()
const callOwners = new Map<string, Socket>()
const toolSockets = new Set<Socket>()
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
  let exchangeId = 'runtime'
  void (async () => {
    try {
      const frame = parseEnvelope(line)
      exchangeId = frame.exchange_id
      await handleFrame(frame)
    } catch (error) {
      writeEvent(exchangeId, { type: 'turn.failed', error: error instanceof Error ? error.message : String(error) })
    }
  })()
})
lines.on('close', () => void shutdown())

async function handleFrame(frame: ReturnType<typeof parseEnvelope>): Promise<void> {
  if (frame.type === 'runtime.shutdown') {
    await shutdown()
    return
  }
  const conversationId = conversationFor(frame)
  await enqueue(conversationId, async () => handleSerial(frame))
}

async function handleSerial(frame: ReturnType<typeof parseEnvelope>): Promise<void> {
  switch (frame.type) {
    case 'turn.start':
      startTurn(frame.exchange_id, frame.payload as TurnRequest)
      return
    case 'turn.resume':
      await resumeTurn(frame.exchange_id, frame.payload as TurnRequest)
      return
    case 'turn.steer':
      await steerTurn(frame.exchange_id, frame.payload as TurnRequest)
      return
    case 'turn.cancel': {
      const payload = frame.payload as { task_id?: unknown; turn_id?: unknown }
      const taskId = payload?.task_id
      if (typeof taskId !== 'string') throw new Error('turn.cancel requires task_id')
      writeEvent(frame.exchange_id, { type: 'turn.cancelling' })
      await cancelTask(taskId, typeof payload.turn_id === 'string' ? payload.turn_id : '')
      writeEvent(frame.exchange_id, { type: 'turn.cancelled' })
      return
    }
    default:
      throw new Error(`unsupported frame type ${frame.type}`)
  }
}

function conversationFor(frame: ReturnType<typeof parseEnvelope>): string {
  const payload = frame.payload as { conversation_id?: unknown; task_id?: unknown } | undefined
  if (typeof payload?.conversation_id === 'string' && payload.conversation_id.length > 0) return payload.conversation_id
  if (typeof payload?.task_id === 'string') {
    const match = [...sessions.entries()].find(([, state]) => state.taskId === payload.task_id)
    return match?.[0] ?? `task:${payload.task_id}`
  }
  return `exchange:${frame.exchange_id}`
}

async function enqueue(conversationId: string, operation: () => Promise<void>): Promise<void> {
  const previous = queues.get(conversationId) ?? Promise.resolve()
  const current = previous.catch(() => undefined).then(operation)
  queues.set(conversationId, current)
  try {
    await current
  } finally {
    if (queues.get(conversationId) === current) queues.delete(conversationId)
  }
}

function startTurn(exchangeId: string, request: TurnRequest): void {
  if (typeof request.turn_id !== 'string' || request.turn_id.length === 0) throw new Error('turn.start requires turn_id')
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
          // The Go adapter gives runtime shutdown five seconds. Keep the
          // SDK's complete protocol -> EOF -> TERM -> KILL ladder below that
          // bound so the sidecar can reap its child instead of being killed
          // first and leaving an orphaned JSON-RPC runtime.
          shutdownTimeoutMs: 500,
          disposeEofGraceMs: 1_500,
          disposeGraceMs: 1_000,
        },
        cwd: process.cwd(),
        provider: process.env.DSH_PROVIDER ?? 'deepseek-official',
        model: process.env.DSH_MODEL ?? 'deepseek-v4-flash',
        ...(positiveInteger(process.env.DSH_MAX_TOKENS) === undefined
          ? {}
          : { maxTokens: positiveInteger(process.env.DSH_MAX_TOKENS) }),
      }),
      exchangeId,
      turnId: request.turn_id,
      taskId: request.task_id,
      running: false,
      sawTextDelta: false,
      pendingSteers: new Map(),
    }
    sessions.set(request.conversation_id, state)
  }
  if (state.running) throw new Error(`conversation ${request.conversation_id} already has a running turn`)
  state.exchangeId = exchangeId
  state.turnId = request.turn_id
  state.taskId = request.task_id
  state.running = true
  state.sawTextDelta = false
  void state.harness.run(prompt, {
    sessionId: request.conversation_id,
    onNotification: notification => onNotification(request.conversation_id, notification),
  }).then(result => {
    const current = sessions.get(request.conversation_id)
    if (current !== state) return
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
    if (current !== state) return
    current.running = false
    writeEvent(current.exchangeId, {
      type: 'turn.failed',
      error: error instanceof Error ? error.message : String(error),
    })
  })
}

async function resumeTurn(exchangeId: string, request: TurnRequest): Promise<void> {
  const state = sessions.get(request.conversation_id)
  if (state === undefined || !state.running) throw new Error(`conversation ${request.conversation_id} has no suspended turn`)
  assertCurrentTurn(state, request)
  state.exchangeId = exchangeId
  state.taskId = request.task_id
  for (const input of request.inputs) {
    if (input.kind !== 'tool.result' || input.tool_call_id === undefined) continue
    const owner = callOwners.get(input.tool_call_id)
    if (owner === undefined) throw new Error(`unknown external tool call ${input.tool_call_id}`)
    callOwners.delete(input.tool_call_id)
    const content = input.status === 'rejected'
      ? `The user rejected this tool call. ${input.content}`.trim()
      : input.content
    owner.write(`${JSON.stringify({ type: 'tool.result', id: input.tool_call_id, content })}\n`)
  }
  const steer = request.inputs.filter(input => input.kind === 'user.steer').map(input => input.content).join('\n\n')
  if (steer.length > 0) {
    await state.harness.start()
    await state.harness.client.prompt(request.conversation_id, [{ type: 'text', text: steer }])
  }
}

async function steerTurn(exchangeId: string, request: TurnRequest): Promise<void> {
  const state = sessions.get(request.conversation_id)
  if (state === undefined || !state.running) throw new Error(`conversation ${request.conversation_id} has no running turn to steer`)
  assertCurrentTurn(state, request)
  const inputs = request.inputs.filter(input => input.kind === 'user.steer')
  const prompt = inputs.map(input => input.content).join('\n\n')
  if (prompt.length === 0) throw new Error('turn.steer requires a steering message')
  const steerId = inputs[0]?.steer_id
  if (typeof steerId !== 'string' || steerId.length === 0) throw new Error('turn.steer requires steer_id')
  await state.harness.start()
  const messageId = await state.harness.client.prompt(request.conversation_id, [{ type: 'text', text: prompt }])
  state.pendingSteers.set(messageId, steerId)
  writeEvent(exchangeId, { type: 'turn.steer.accepted', steer_id: steerId })
}

function assertCurrentTurn(state: SessionState, request: TurnRequest): void {
  if (typeof request.turn_id !== 'string' || request.turn_id.length === 0 || state.turnId !== request.turn_id) {
    throw new Error(`stale turn ${request.turn_id} for conversation ${request.conversation_id}`)
  }
}

function onNotification(conversationId: string, notification: HarnessNotification): void {
  if (notification.method !== 'session.event') return
  const state = sessions.get(conversationId)
  if (state === undefined) return
  const event = notification.params.event as { type?: string; data?: Record<string, unknown> } | undefined
  if (event?.type === 'user/message') {
    const messageId = event.data?.id
    if (typeof messageId === 'string') {
      const steerId = state.pendingSteers.get(messageId)
      if (steerId !== undefined) {
        state.pendingSteers.delete(messageId)
        writeEvent(state.exchangeId, { type: 'turn.steer.applied', steer_id: steerId })
      }
    }
    return
  }
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
	toolSockets.add(socket)
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
      const toolCalls = []
      for (const call of message.calls) {
        try {
          toolCalls.push({
            id: call.id,
            name: workspaceToolName(call.name),
            arguments: workspaceToolArguments(call.name, call.arguments),
          })
          callOwners.set(call.id, socket)
        } catch (error) {
          const content = error instanceof Error ? error.message : String(error)
          socket.write(`${JSON.stringify({ type: 'tool.result', id: call.id, content, is_error: true })}\n`)
        }
      }
      if (toolCalls.length > 0) {
        writeEvent(state.exchangeId, { type: 'tool.call.batch', tool_calls: toolCalls })
        writeEvent(state.exchangeId, { type: 'turn.awaiting_tool' })
      }
    }
  })
	socket.on('close', () => {
		toolSockets.delete(socket)
		for (const [id, owner] of callOwners) {
      if (owner === socket) callOwners.delete(id)
    }
  })
}

async function cancelTask(taskId: string, turnId: string): Promise<void> {
  const matching = [...sessions.entries()].filter(([, state]) =>
    state.taskId === taskId && (turnId.length === 0 || state.turnId === turnId))
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
		// Yield once so shutdownPromise is assigned before lines.close()
		// synchronously emits its own close event and re-enters shutdown().
		await Promise.resolve()
		lines.close()
		// Stop accepting connections immediately, but do not await the close
		// callback yet: it only fires after existing tool sockets close, and those
		// sockets are owned by the Harness children we still need to shut down.
		const serverClosed = new Promise<void>((resolve, reject) => {
			toolServer.close(error => error === undefined ? resolve() : reject(error))
		})
		const results = await Promise.allSettled([...sessions.values()].map(state => state.harness.close()))
		sessions.clear()
		callOwners.clear()
		for (const socket of toolSockets) socket.destroy()
		await serverClosed
		for (const result of results) {
			if (result.status === 'rejected') console.error(`[shutdown] failed to close Harness child: ${String(result.reason)}`)
		}
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
    bash_output: 'workspace.process.read',
    bash_write: 'workspace.process.write',
    bash_cancel: 'workspace.process.cancel',
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
  if (process.env.DSH_RUNTIME_SKIP_NODE_CHECK === 'true') return
  const [major = 0, minor = 0] = process.versions.node.split('.').map(Number)
  if (major >= 24 || (major === 22 && minor >= 19)) return
  throw new Error(`DeepSeek Harness requires Node ^22.19 or >=24; current runtime is ${process.versions.node}`)
}
