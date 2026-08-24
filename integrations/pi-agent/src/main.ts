#!/usr/bin/env node
import { createInterface } from 'node:readline'
import { envelope, parseEnvelope, type RuntimeEvent } from './protocol.js'
import { PiAgentRuntime } from './runtime.js'

assertSupportedNode()

const writeEvent = (exchangeId: string, event: RuntimeEvent): void => {
  process.stdout.write(`${JSON.stringify(envelope(exchangeId, 'event', event))}\n`)
}
const runtime = new PiAgentRuntime(writeEvent)
const lines = createInterface({ input: process.stdin, crlfDelay: Infinity })
let shutdownPromise: Promise<void> | undefined

lines.on('line', line => {
  if (line.trim().length === 0) return
  let exchangeId = 'runtime'
  void (async () => {
    try {
      const frame = parseEnvelope(line)
      exchangeId = frame.exchange_id
      await runtime.handle(frame)
      if (frame.type === 'runtime.shutdown') await shutdown()
    } catch (error) {
      writeEvent(exchangeId, { type: 'turn.failed', error: error instanceof Error ? error.message : String(error) })
    }
  })()
})
lines.on('close', () => void shutdown())

async function shutdown(): Promise<void> {
  if (shutdownPromise !== undefined) return shutdownPromise
  shutdownPromise = (async () => {
    await Promise.resolve()
    lines.close()
    await runtime.shutdown()
  })()
  return shutdownPromise
}

function assertSupportedNode(): void {
  const [major = 0, minor = 0] = process.versions.node.split('.').map(Number)
  if (major > 22 || (major === 22 && minor >= 19)) return
  throw new Error(`Pi requires Node >=22.19; current runtime is ${process.versions.node}`)
}
