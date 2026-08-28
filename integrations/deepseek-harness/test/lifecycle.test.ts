import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { join } from 'node:path'
import test from 'node:test'
import { envelope, parseEnvelope } from '../src/protocol.js'

test('DSH serializes idempotent cancellation acknowledgements', { timeout: 10_000 }, async () => {
  const child = spawn(join(process.cwd(), 'node_modules', '.bin', 'tsx'), ['src/main.ts'], {
    cwd: process.cwd(), stdio: ['pipe', 'pipe', 'pipe'],
    env: { ...process.env, DSH_RUNTIME_SKIP_NODE_CHECK: 'true' },
  })
  const events: Array<{ exchangeId: string; type: string }> = []
  let buffer = ''
  let complete: (() => void) | undefined
  const done = new Promise<void>(resolve => { complete = resolve })
  child.stdout.setEncoding('utf8')
  child.stdout.on('data', chunk => {
    buffer += chunk
    while (true) {
      const newline = buffer.indexOf('\n')
      if (newline < 0) break
      const line = buffer.slice(0, newline)
      buffer = buffer.slice(newline + 1)
      if (line.trim().length === 0) continue
      const frame = parseEnvelope(line)
      const type = (frame.payload as { type?: unknown })?.type
      if (typeof type === 'string') events.push({ exchangeId: frame.exchange_id, type })
      if (events.filter(event => event.type === 'turn.cancelled').length === 2) complete?.()
    }
  })

  try {
    child.stdin.write(`${JSON.stringify(envelope('cancel-1', 'turn.cancel', {
      conversation_id: 'conversation-1', turn_id: 'missing-turn', task_id: 'missing-task',
    }))}\n`)
    child.stdin.write(`${JSON.stringify(envelope('cancel-2', 'turn.cancel', {
      conversation_id: 'conversation-1', turn_id: 'missing-turn', task_id: 'missing-task',
    }))}\n`)
    await done
    assert.deepEqual(events, [
      { exchangeId: 'cancel-1', type: 'turn.cancelling' },
      { exchangeId: 'cancel-1', type: 'turn.cancelled' },
      { exchangeId: 'cancel-2', type: 'turn.cancelling' },
      { exchangeId: 'cancel-2', type: 'turn.cancelled' },
    ])
  } finally {
    child.stdin.write(`${JSON.stringify(envelope('shutdown', 'runtime.shutdown', {}))}\n`)
    child.stdin.end()
    await new Promise<void>(resolve => child.once('exit', () => resolve()))
  }
})
