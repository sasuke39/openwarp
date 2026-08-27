import assert from 'node:assert/strict'
import test from 'node:test'
import { workspaceToolArguments } from '../src/workspace-tool-arguments.js'

test('DSH bash normalizes explicit background execution', () => {
  assert.deepEqual(workspaceToolArguments('bash', {
    command: './start.sh', workdir: '/srv/app', run_in_background: true,
  }), {
    command: './start.sh', workdir: '/srv/app', executionMode: 'background',
  })
})

test('DSH bash defaults to auto and preserves explicit foreground execution', () => {
  assert.deepEqual(workspaceToolArguments('bash', { command: 'pwd' }), {
    command: 'pwd', executionMode: 'auto',
  })
  assert.deepEqual(workspaceToolArguments('bash', { command: 'make', run_in_background: false }), {
    command: 'make', executionMode: 'foreground',
  })
})
