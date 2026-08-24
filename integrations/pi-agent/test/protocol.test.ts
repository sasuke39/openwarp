import assert from 'node:assert/strict'
import test from 'node:test'
import { envelope, parseEnvelope } from '../src/protocol.js'

test('protocol envelope round trips', () => {
  const original = envelope('exchange-1', 'event', { type: 'turn.completed' })
  assert.deepEqual(parseEnvelope(JSON.stringify(original)), original)
})

test('protocol rejects incompatible versions', () => {
  assert.throws(() => parseEnvelope('{"version":2,"exchange_id":"x","type":"event"}'), /unsupported protocol version/)
})
