import assert from 'node:assert/strict'
import test from 'node:test'
import { WorkspaceToolBroker, type ToolOwner } from '../src/workspace-tools.js'
import type { RuntimeEvent } from '../src/protocol.js'

test('broker batches calls and resumes them by id', async () => {
  const events: RuntimeEvent[] = []
  const owner: ToolOwner = {
    conversationId: 'conversation-1', workingDir: '/tmp', active: true,
    emit: event => events.push(event),
  }
  const broker = new WorkspaceToolBroker()
  const first = broker.execute(owner, 'workspace.shell', { command: 'pwd' })
  const second = broker.execute(owner, 'workspace.read_file', { file_path: 'README.md' })
  await new Promise(resolve => setImmediate(resolve))

  assert.equal(events[0]?.type, 'tool.call.batch')
  assert.equal(events[1]?.type, 'turn.awaiting_tool')
  const calls = events[0]?.tool_calls ?? []
  assert.equal(calls.length, 2)
  assert.equal(broker.deliver(calls[0].id, '/tmp'), true)
  assert.equal(broker.deliver(calls[1].id, 'readme'), true)
  assert.deepEqual(await Promise.all([first, second]), ['/tmp', 'readme'])
})

test('broker rejects pending calls when owner is cancelled', async () => {
  const owner: ToolOwner = {
    conversationId: 'conversation-1', workingDir: '/tmp', active: true, emit: () => undefined,
  }
  const broker = new WorkspaceToolBroker()
  const pending = broker.execute(owner, 'workspace.shell', { command: 'sleep 1' })
  broker.cancel(owner, 'cancelled')
  await assert.rejects(pending, /cancelled/)
})
