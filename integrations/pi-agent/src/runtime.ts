import { createHash } from 'node:crypto'
import { mkdir } from 'node:fs/promises'
import { homedir, tmpdir } from 'node:os'
import { join } from 'node:path'
import {
  createAgentSession,
  DefaultResourceLoader,
  ModelRuntime,
  SessionManager,
  SettingsManager,
  type AgentSession,
  type AgentSessionEvent,
} from '@earendil-works/pi-coding-agent'
import type { Envelope, RuntimeEvent, TurnRequest } from './protocol.js'
import { createWorkspaceTools, WorkspaceToolBroker, type ToolOwner } from './workspace-tools.js'

interface SessionState extends ToolOwner {
  session: AgentSession
  settings: SettingsManager
  exchangeId: string
  turnId: string
  taskId: string
  runToken: number
  sawAssistantText: boolean
  pendingSteers: Array<{ id: string; prompt: string }>
  unsubscribe(): void
}

export class PiAgentRuntime {
  private readonly sessions = new Map<string, SessionState>()
  private readonly queues = new Map<string, Promise<void>>()
  private readonly broker = new WorkspaceToolBroker()
  private modelRuntimePromise: Promise<ModelRuntime> | undefined

  constructor(private readonly emitFrame: (exchangeId: string, event: RuntimeEvent) => void) {}

  async handle(frame: Envelope): Promise<void> {
    if (frame.type === 'runtime.shutdown') {
      await this.shutdown()
      return
    }
    const conversationId = this.conversationFor(frame)
    await this.enqueue(conversationId, async () => this.handleSerial(frame))
  }

  private async handleSerial(frame: Envelope): Promise<void> {
    switch (frame.type) {
      case 'turn.start':
        await this.startTurn(frame.exchange_id, frame.payload as TurnRequest)
        return
      case 'turn.resume':
        await this.resumeTurn(frame.exchange_id, frame.payload as TurnRequest)
        return
      case 'turn.steer':
        await this.steerTurn(frame.exchange_id, frame.payload as TurnRequest)
        return
      case 'turn.cancel': {
        const payload = frame.payload as { task_id?: unknown; turn_id?: unknown }
        const taskId = payload?.task_id
        if (typeof taskId !== 'string' || taskId.length === 0) throw new Error('turn.cancel requires task_id')
        this.emitFrame(frame.exchange_id, { type: 'turn.cancelling' })
        await this.cancelTask(taskId, typeof payload.turn_id === 'string' ? payload.turn_id : '')
        this.emitFrame(frame.exchange_id, { type: 'turn.cancelled' })
        return
      }
      default:
        throw new Error(`unsupported frame type ${frame.type}`)
    }
  }

  private conversationFor(frame: Envelope): string {
    const payload = frame.payload as { conversation_id?: unknown; task_id?: unknown } | undefined
    if (typeof payload?.conversation_id === 'string' && payload.conversation_id.length > 0) return payload.conversation_id
    if (typeof payload?.task_id === 'string') {
      const match = [...this.sessions.values()].find(state => state.taskId === payload.task_id)
      if (match !== undefined) return match.conversationId
      return `task:${payload.task_id}`
    }
    return `exchange:${frame.exchange_id}`
  }

  private async enqueue(conversationId: string, operation: () => Promise<void>): Promise<void> {
    const previous = this.queues.get(conversationId) ?? Promise.resolve()
    const current = previous.catch(() => undefined).then(operation)
    this.queues.set(conversationId, current)
    try {
      await current
    } finally {
      if (this.queues.get(conversationId) === current) this.queues.delete(conversationId)
    }
  }

  async shutdown(): Promise<void> {
    const states = [...this.sessions.values()]
    this.sessions.clear()
    await Promise.all(states.map(state => this.disposeState(state, 'Pi runtime is shutting down')))
  }

  private async startTurn(exchangeId: string, request: TurnRequest): Promise<void> {
    if (typeof request.turn_id !== 'string' || request.turn_id.length === 0) throw new Error('turn.start requires turn_id')
    const prompt = request.inputs.filter(input => input.kind === 'user.message').map(input => input.content).join('\n\n')
    if (prompt.length === 0) throw new Error('turn.start requires a user message')

    let state = this.sessions.get(request.conversation_id)
    if (state === undefined) {
      state = await this.createState(exchangeId, request)
      this.sessions.set(request.conversation_id, state)
    }
    // Warp may submit a fresh user message after rejecting a pending tool. A
    // rejected tool does not always produce a resume exchange, so make a new
    // turn authoritative and release the stale Pi prompt before starting it.
    if (state.active) await this.cancelState(state, 'Pi turn superseded by new user input')
    state.exchangeId = exchangeId
    state.turnId = request.turn_id
    state.taskId = request.task_id
    state.workingDir = request.working_dir || state.workingDir
    state.active = true
    state.sawAssistantText = false
    const runToken = ++state.runToken
    void this.runPrompt(state, runToken, prompt)
  }

  private async resumeTurn(exchangeId: string, request: TurnRequest): Promise<void> {
    const state = this.sessions.get(request.conversation_id)
    if (state === undefined || !state.active) throw new Error(`conversation ${request.conversation_id} has no suspended turn`)
    this.assertCurrentTurn(state, request)
    state.exchangeId = exchangeId
    state.taskId = request.task_id
    state.workingDir = request.working_dir || state.workingDir
    let delivered = 0
    for (const input of request.inputs) {
      if (input.kind !== 'tool.result' || input.tool_call_id === undefined) continue
      const content = input.status === 'rejected'
        ? `The user rejected this tool call. ${input.content}`.trim()
        : input.content
      if (!this.broker.deliver(input.tool_call_id, content)) {
        throw new Error(`unknown external tool call ${input.tool_call_id}`)
      }
      delivered++
    }
    if (delivered === 0) throw new Error('turn.resume requires at least one tool result')
    const steer = request.inputs.filter(input => input.kind === 'user.steer').map(input => input.content).join('\n\n')
    if (steer.length > 0) await state.session.steer(steer)
  }

  private async steerTurn(exchangeId: string, request: TurnRequest): Promise<void> {
    const state = this.sessions.get(request.conversation_id)
    if (state === undefined || !state.active) throw new Error(`conversation ${request.conversation_id} has no running turn to steer`)
    this.assertCurrentTurn(state, request)
    const inputs = request.inputs.filter(input => input.kind === 'user.steer')
    const prompt = inputs.map(input => input.content).join('\n\n')
    if (prompt.length === 0) throw new Error('turn.steer requires a steering message')
    const steerId = inputs[0]?.steer_id
    if (typeof steerId !== 'string' || steerId.length === 0) throw new Error('turn.steer requires steer_id')
    state.pendingSteers.push({ id: steerId, prompt })
    try {
      await state.session.steer(prompt)
    } catch (error) {
      state.pendingSteers = state.pendingSteers.filter(item => item.id !== steerId)
      throw error
    }
    this.emitFrame(exchangeId, { type: 'turn.steer.accepted', steer_id: steerId })
  }

  private assertCurrentTurn(state: SessionState, request: TurnRequest): void {
    if (typeof request.turn_id !== 'string' || request.turn_id.length === 0 || state.turnId !== request.turn_id) {
      throw new Error(`stale turn ${request.turn_id} for conversation ${request.conversation_id}`)
    }
  }

  private async cancelTask(taskId: string, turnId: string): Promise<void> {
    const matching = [...this.sessions.values()].filter(state =>
      state.taskId === taskId && state.active && (turnId.length === 0 || state.turnId === turnId))
    await Promise.all(matching.map(state => this.cancelState(state, 'Pi task was cancelled')))
  }

  private async cancelState(state: SessionState, reason: string): Promise<void> {
    state.runToken++
    this.broker.cancel(state, reason)
    await state.session.abort()
    await state.session.waitForIdle()
    state.active = false
  }

  private async createState(exchangeId: string, request: TurnRequest): Promise<SessionState> {
    const workingDir = request.working_dir || process.cwd()
    const agentDir = process.env.PI_AGENT_DIR ?? join(homedir(), '.pi', 'agent')
    const sessionRoot = process.env.PI_SESSION_ROOT ?? join(tmpdir(), 'open-warp-pi-sessions')
    const sessionDir = join(sessionRoot, createHash('sha256').update(request.conversation_id).digest('hex'))
    await mkdir(sessionDir, { recursive: true })

    const settings = SettingsManager.inMemory({
      compaction: deriveCompactionSettings(
        positiveInteger(process.env.AGENT_RUNTIME_CONTEXT_WINDOW) ?? 128000,
        positiveInteger(process.env.AGENT_RUNTIME_MAX_TOKENS) ?? 16384,
      ),
      retry: { enabled: true, maxRetries: positiveInteger(process.env.PI_MAX_RETRIES) ?? 3 },
    })
    const enableExtensions = process.env.PI_ENABLE_EXTENSIONS === 'true'
    const loader = new DefaultResourceLoader({
      cwd: workingDir,
      agentDir,
      settingsManager: settings,
      noExtensions: !enableExtensions,
      noContextFiles: true,
      appendSystemPrompt: [request.system_prompt ?? 'Operate in the active Warp terminal through the provided tools.'],
    })
    await loader.reload({ resolveProjectTrust: async () => enableExtensions })

    const owner = {
      conversationId: request.conversation_id,
      workingDir,
      active: false,
      emit: (event: RuntimeEvent) => {
        const current = this.sessions.get(request.conversation_id)
        if (current === state) this.emitFrame(current.exchangeId, event)
      },
    }
    const modelRuntime = await this.modelRuntime()
    const model = modelRuntime.getModel('open-warp', requiredEnv('AGENT_RUNTIME_MODEL'))
    if (model === undefined) throw new Error(`Pi model ${requiredEnv('AGENT_RUNTIME_MODEL')} was not registered`)
    const result = await createAgentSession({
      cwd: workingDir,
      agentDir,
      modelRuntime,
      model,
      thinkingLevel: process.env.AGENT_RUNTIME_THINKING_DISABLED === 'true' ? 'off' : 'medium',
      resourceLoader: loader,
      settingsManager: settings,
      sessionManager: SessionManager.continueRecent(workingDir, sessionDir),
      tools: ['bash', 'read', 'write', 'edit', 'grep', 'find', 'ls'],
      customTools: createWorkspaceTools(owner, this.broker),
    })
    const state = owner as SessionState
    state.session = result.session
    state.settings = settings
    state.exchangeId = exchangeId
    state.turnId = request.turn_id
    state.taskId = request.task_id
    state.runToken = 0
    state.sawAssistantText = false
    state.pendingSteers = []
    state.unsubscribe = result.session.subscribe(event => this.onSessionEvent(state, event))
    return state
  }

  private async modelRuntime(): Promise<ModelRuntime> {
    if (this.modelRuntimePromise !== undefined) return this.modelRuntimePromise
    this.modelRuntimePromise = (async () => {
      const runtime = await ModelRuntime.create({ modelsPath: null, refreshOnCreate: false })
      const maxTokens = positiveInteger(process.env.AGENT_RUNTIME_MAX_TOKENS) ?? 16384
			const contextWindow = Math.max(positiveInteger(process.env.AGENT_RUNTIME_CONTEXT_WINDOW) ?? 128000, maxTokens)
      runtime.registerProvider('open-warp', {
        name: 'OpenWarp OpenAI-compatible provider',
        baseUrl: requiredEnv('AGENT_RUNTIME_BASE_URL'),
        apiKey: requiredEnv('AGENT_RUNTIME_API_KEY'),
        api: 'openai-completions',
        models: [{
          id: requiredEnv('AGENT_RUNTIME_MODEL'),
          name: requiredEnv('AGENT_RUNTIME_MODEL'),
          reasoning: process.env.AGENT_RUNTIME_THINKING_DISABLED !== 'true',
          input: ['text', 'image'],
          cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
          contextWindow,
          maxTokens,
          compat: { supportsDeveloperRole: false, supportsReasoningEffort: false },
        }],
      })
      return runtime
    })()
    return this.modelRuntimePromise
  }

  private async runPrompt(state: SessionState, runToken: number, prompt: string): Promise<void> {
    try {
      await state.session.prompt(prompt)
      if (!state.active || state.runToken !== runToken) return
      state.active = false
      const failure = lastAssistantFailure(state.session.messages)
      if (failure !== undefined) {
        state.emit({ type: 'turn.failed', error: failure })
      } else if (!state.sawAssistantText) {
        state.emit({ type: 'turn.failed', error: 'Pi completed without an assistant response' })
      } else {
        state.emit({ type: 'turn.completed' })
      }
    } catch (error) {
      if (!state.active || state.runToken !== runToken) return
      state.active = false
      state.emit({ type: 'turn.failed', error: errorMessage(error) })
    }
  }

  private onSessionEvent(state: SessionState, event: AgentSessionEvent): void {
    if (!state.active) return
    if (event.type === 'message_start') {
      const prompt = userText(event.message)
      const index = state.pendingSteers.findIndex(item => item.prompt === prompt)
      if (index >= 0) {
        const [steer] = state.pendingSteers.splice(index, 1)
        state.emit({ type: 'turn.steer.applied', steer_id: steer.id })
      }
      return
    }
    if (event.type === 'message_update' && event.assistantMessageEvent.type === 'text_delta') {
      state.sawAssistantText = true
      state.emit({ type: 'assistant.delta', text: event.assistantMessageEvent.delta })
      return
    }
    if (event.type === 'message_end' && !state.sawAssistantText) {
      const text = assistantText(event.message)
      if (text.length > 0) {
        state.sawAssistantText = true
        state.emit({ type: 'assistant.final', text })
      }
      return
    }
    if (event.type === 'auto_retry_start') {
      state.emit({ type: 'diagnostic', text: `Pi retry ${event.attempt}/${event.maxAttempts} in ${event.delayMs}ms: ${event.errorMessage}` })
      return
    }
    if (event.type === 'compaction_start') {
      state.emit({ type: 'diagnostic', text: `Pi compaction started (${event.reason})` })
    }
  }

  private async disposeState(state: SessionState, reason: string): Promise<void> {
    state.active = false
    state.runToken++
    this.broker.cancel(state, reason)
    await state.session.abort()
    state.unsubscribe()
    state.session.dispose()
    await state.settings.flush()
  }
}

function userText(message: unknown): string {
  if (typeof message !== 'object' || message === null || (message as { role?: unknown }).role !== 'user') return ''
  const content = (message as { content?: unknown }).content
  if (typeof content === 'string') return content
  if (!Array.isArray(content)) return ''
  return content.flatMap(item => {
    if (typeof item !== 'object' || item === null) return []
    const block = item as { type?: unknown; text?: unknown }
    return block.type === 'text' && typeof block.text === 'string' ? [block.text] : []
  }).join('')
}

function assistantText(message: unknown): string {
  if (typeof message !== 'object' || message === null || (message as { role?: unknown }).role !== 'assistant') return ''
  const content = (message as { content?: unknown }).content
  if (!Array.isArray(content)) return ''
  return content.flatMap(item => {
    if (typeof item !== 'object' || item === null) return []
    const block = item as { type?: unknown; text?: unknown }
    return block.type === 'text' && typeof block.text === 'string' ? [block.text] : []
  }).join('')
}

function lastAssistantFailure(messages: readonly unknown[]): string | undefined {
  for (let index = messages.length - 1; index >= 0; index--) {
    const message = messages[index]
    if (typeof message !== 'object' || message === null || (message as { role?: unknown }).role !== 'assistant') continue
    const assistant = message as { stopReason?: unknown; errorMessage?: unknown }
    if (assistant.stopReason === 'error' || assistant.stopReason === 'aborted') {
      return typeof assistant.errorMessage === 'string' ? assistant.errorMessage : `Pi stopped with ${assistant.stopReason}`
    }
    if (assistant.stopReason === 'length') {
      return 'Pi reached the model output/context limit after compaction recovery; the turn was released and a new turn may be started'
    }
    return undefined
  }
  return undefined
}

export function deriveCompactionSettings(contextWindow: number, maxOutputTokens: number): {
  enabled: true
  reserveTokens: number
  keepRecentTokens: number
} {
  // Pi's defaults (16k reserve + 20k recent history) cannot produce a legal
  // compaction plan for a 32k model. Keep both budgets proportional on small
  // windows, while retaining Pi's defaults for large-context models.
  const reserveTokens = Math.min(16384, maxOutputTokens, Math.max(2048, Math.floor(contextWindow / 4)))
  const keepRecentTokens = Math.min(20000, Math.max(2048, Math.floor(contextWindow / 4)))
  return { enabled: true, reserveTokens, keepRecentTokens }
}

function requiredEnv(name: string): string {
  const value = process.env[name]?.trim()
  if (value === undefined || value.length === 0) throw new Error(`${name} is required for Pi runtime`)
  return value
}

function positiveInteger(raw: string | undefined): number | undefined {
  if (raw === undefined || raw.length === 0) return undefined
  const value = Number(raw)
  return Number.isSafeInteger(value) && value > 0 ? value : undefined
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}
