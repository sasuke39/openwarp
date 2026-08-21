import assert from 'node:assert/strict'
import test from 'node:test'
import { envelope, parseEnvelope } from '../src/protocol.js'

test('protocol envelope round trips', () => {
  const original = envelope('exchange-1', 'event', { type: 'turn.completed' })
  const decoded = parseEnvelope(JSON.stringify(original))
  assert.deepEqual(decoded, original)
})

test('protocol rejects incompatible versions', () => {
  assert.throws(() => parseEnvelope('{"version":2,"exchange_id":"x","type":"event"}'), /unsupported protocol version/)
})
