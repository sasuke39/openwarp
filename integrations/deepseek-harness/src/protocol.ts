export const PROTOCOL_VERSION = 1

export interface Envelope<T = unknown> {
  version: number
  exchange_id: string
  type: string
  payload?: T
}

export interface RuntimeInput {
  kind: 'user.message' | 'tool.result'
  content: string
  tool_call_id?: string
}

export interface TurnRequest {
  conversation_id: string
  task_id: string
  request_id: string
  system_prompt?: string
  working_dir?: string
  inputs: RuntimeInput[]
  metadata?: Record<string, string>
}

export interface ExternalToolCall {
  id: string
  session_id: string
  name: string
  arguments: unknown
}

export interface ExternalToolResult {
  type: 'tool.result'
  id: string
  content: string
  is_error?: boolean
}

export interface RuntimeEvent {
  type: string
  text?: string
  error?: string
  tool_calls?: Array<{ id: string; name: string; arguments: unknown }>
  data?: unknown
}

export function envelope<T>(exchangeId: string, type: string, payload: T): Envelope<T> {
  return { version: PROTOCOL_VERSION, exchange_id: exchangeId, type, payload }
}

export function parseEnvelope(line: string): Envelope {
  const value: unknown = JSON.parse(line)
  if (typeof value !== 'object' || value === null) throw new Error('frame must be an object')
  const frame = value as Partial<Envelope>
  if (frame.version !== PROTOCOL_VERSION) throw new Error(`unsupported protocol version ${String(frame.version)}`)
  if (typeof frame.exchange_id !== 'string' || frame.exchange_id.length === 0) throw new Error('exchange_id is required')
  if (typeof frame.type !== 'string' || frame.type.length === 0) throw new Error('type is required')
  return frame as Envelope
}
