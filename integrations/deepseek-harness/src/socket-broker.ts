import { createConnection, type Socket } from 'node:net'
import { StringDecoder } from 'node:string_decoder'
import type { ExternalToolCall, ExternalToolResult } from './protocol.js'

interface PendingCall {
  call: ExternalToolCall
  resolve: (result: ExternalToolResult) => void
  reject: (error: Error) => void
}

export class ExternalToolBroker {
  private socket: Socket | undefined
  private readonly pending = new Map<string, PendingCall>()
  private readonly queued: PendingCall[] = []
  private flushScheduled = false
  private buffer = ''

  constructor(private readonly socketPath: string) {}

  async execute(call: ExternalToolCall, signal: AbortSignal): Promise<ExternalToolResult> {
    await this.connect()
    return await new Promise<ExternalToolResult>((resolve, reject) => {
      const pending = { call, resolve, reject }
      this.pending.set(call.id, pending)
      this.queued.push(pending)
      this.scheduleFlush()
      const abort = (): void => {
        if (!this.pending.delete(call.id)) return
        reject(new Error('external tool call aborted'))
        this.write({ type: 'tool.cancel', id: call.id })
      }
      if (signal.aborted) abort()
      else signal.addEventListener('abort', abort, { once: true })
    })
  }

  close(): void {
    this.socket?.destroy()
    this.socket = undefined
    this.failAll(new Error('external tool broker closed'))
  }

  private async connect(): Promise<void> {
    if (this.socket !== undefined && !this.socket.destroyed) return
    await new Promise<void>((resolve, reject) => {
      const socket = createConnection(this.socketPath)
      const onError = (error: Error): void => reject(error)
      socket.once('error', onError)
      socket.once('connect', () => {
        socket.off('error', onError)
        this.socket = socket
        this.attach(socket)
        resolve()
      })
    })
  }

  private attach(socket: Socket): void {
    const decoder = new StringDecoder('utf8')
    socket.on('data', (chunk: Buffer) => {
      this.buffer += decoder.write(chunk)
      this.consumeLines()
    })
    socket.on('close', () => {
      if (this.socket === socket) this.socket = undefined
      this.failAll(new Error('external tool bridge disconnected'))
    })
    socket.on('error', () => undefined)
  }

  private consumeLines(): void {
    while (true) {
      const newline = this.buffer.indexOf('\n')
      if (newline < 0) return
      const line = this.buffer.slice(0, newline)
      this.buffer = this.buffer.slice(newline + 1)
      if (line.trim().length === 0) continue
      const result = JSON.parse(line) as ExternalToolResult
      if (result.type !== 'tool.result' || typeof result.id !== 'string') continue
      const pending = this.pending.get(result.id)
      if (pending === undefined) continue
      this.pending.delete(result.id)
      pending.resolve(result)
    }
  }

  private scheduleFlush(): void {
    if (this.flushScheduled) return
    this.flushScheduled = true
    queueMicrotask(() => {
      this.flushScheduled = false
      const calls = this.queued.splice(0).filter(item => this.pending.has(item.call.id)).map(item => item.call)
      if (calls.length > 0) this.write({ type: 'tool.call.batch', calls })
    })
  }

  private write(value: unknown): void {
    this.socket?.write(`${JSON.stringify(value)}\n`)
  }

  private failAll(error: Error): void {
    const pending = [...this.pending.values()]
    this.pending.clear()
    this.queued.length = 0
    for (const item of pending) item.reject(error)
  }
}
