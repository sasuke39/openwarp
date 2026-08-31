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

test('DSH bash requires an explicit execution mode', () => {
  assert.throws(
    () => workspaceToolArguments('bash', { command: 'pwd' }),
    /explicitly select foreground or background/,
  )
  assert.deepEqual(workspaceToolArguments('bash', { command: 'make', run_in_background: false }), {
    command: 'make', executionMode: 'foreground',
  })
})

test('DSH rejects shell-managed background syntax declared as foreground', () => {
  assert.throws(
    () => workspaceToolArguments('bash', {
      command: 'nohup npm run deploy >deploy.log 2>&1 & echo started',
      run_in_background: false,
    }),
    /run_in_background=true/,
  )
  assert.doesNotThrow(() => workspaceToolArguments('bash', {
    command: 'npm run deploy', run_in_background: true,
  }))
})

test('DSH process tools normalize command ids', () => {
  assert.deepEqual(workspaceToolArguments('bash_output', { command_id: 'job-1' }), {
    commandId: 'job-1',
  })
  assert.deepEqual(workspaceToolArguments('bash_write', { command_id: 'job-1', input: 'yes\n' }), {
    commandId: 'job-1', input: 'yes\n',
  })
})
