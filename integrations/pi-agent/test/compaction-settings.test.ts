import assert from 'node:assert/strict'
import test from 'node:test'
import { deriveCompactionSettings } from '../src/runtime.js'

test('small context windows receive a compactable Pi budget', () => {
  assert.deepEqual(deriveCompactionSettings(32_000, 16_384), {
    enabled: true,
    reserveTokens: 8_000,
    keepRecentTokens: 8_000,
  })
})

test('large context windows retain bounded Pi defaults', () => {
  assert.deepEqual(deriveCompactionSettings(1_000_000, 16_384), {
    enabled: true,
    reserveTokens: 16_384,
    keepRecentTokens: 20_000,
  })
})
