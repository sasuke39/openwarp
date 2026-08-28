import assert from 'node:assert/strict'
import { mkdtemp } from 'node:fs/promises'
import { createServer } from 'node:http'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import test from 'node:test'
import { envelope, type RuntimeEvent, type TurnRequest } from '../src/protocol.js'
import { PiAgentRuntime } from '../src/runtime.js'

test('Pi runtime suspends for a Warp tool and resumes the SDK session', { timeout: 20_000 }, async () => {
	const root = await mkdtemp(join(tmpdir(), 'open-warp-pi-smoke-'))
	let requestCount = 0
	const server = createServer((_request, response) => {
		requestCount++
		response.writeHead(200, { 'content-type': 'text/event-stream' })
		if (requestCount === 1) {
			response.write(`data: ${JSON.stringify({
				id: 'chatcmpl-tool', object: 'chat.completion.chunk', created: 1, model: 'test-model',
				choices: [{ index: 0, delta: {
					role: 'assistant',
					tool_calls: [{ index: 0, id: 'pi-call-1', type: 'function', function: { name: 'bash', arguments: '{"command":"pwd","execution_mode":"foreground"}' } }],
				}, finish_reason: null }],
			})}\n\n`)
			response.write(`data: ${JSON.stringify({
				id: 'chatcmpl-tool', object: 'chat.completion.chunk', created: 1, model: 'test-model',
				choices: [{ index: 0, delta: {}, finish_reason: 'tool_calls' }],
				usage: { prompt_tokens: 10, completion_tokens: 3, total_tokens: 13 },
			})}\n\n`)
			response.end('data: [DONE]\n\n')
			return
		}
		response.write(`data: ${JSON.stringify({
      id: 'chatcmpl-test', object: 'chat.completion.chunk', created: 1, model: 'test-model',
      choices: [{ index: 0, delta: { role: 'assistant', content: 'Pi bridge works' }, finish_reason: null }],
    })}\n\n`)
    response.write(`data: ${JSON.stringify({
      id: 'chatcmpl-test', object: 'chat.completion.chunk', created: 1, model: 'test-model',
      choices: [{ index: 0, delta: {}, finish_reason: 'stop' }],
      usage: { prompt_tokens: 10, completion_tokens: 3, total_tokens: 13 },
    })}\n\n`)
    response.end('data: [DONE]\n\n')
  })
  await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve))
  const address = server.address()
  if (address === null || typeof address === 'string') throw new Error('fake provider did not bind a TCP port')

  const previous = new Map<string, string | undefined>()
  const env = {
    AGENT_RUNTIME_API_KEY: 'test-key',
    AGENT_RUNTIME_BASE_URL: `http://127.0.0.1:${address.port}/v1`,
    AGENT_RUNTIME_MODEL: 'test-model',
    AGENT_RUNTIME_THINKING_DISABLED: 'true',
    PI_AGENT_DIR: join(root, 'agent'),
    PI_SESSION_ROOT: join(root, 'sessions'),
  }
  for (const [name, value] of Object.entries(env)) {
    previous.set(name, process.env[name])
    process.env[name] = value
  }

	const events: RuntimeEvent[] = []
	let finish: (() => void) | undefined
	let toolReady: (() => void) | undefined
	const terminal = new Promise<void>(resolve => { finish = resolve })
	const awaitingTool = new Promise<void>(resolve => { toolReady = resolve })
	const runtime = new PiAgentRuntime((_exchangeId, event) => {
		events.push(event)
		if (event.type === 'turn.awaiting_tool') toolReady?.()
		if (event.type === 'turn.completed' || event.type === 'turn.failed') finish?.()
  })
  const request: TurnRequest = {
    conversation_id: 'smoke-conversation', task_id: 'smoke-task', request_id: 'smoke-request',
    working_dir: root, inputs: [{ kind: 'user.message', content: 'Say the smoke-test phrase.' }],
  }

	try {
		await runtime.handle(envelope('exchange-1', 'turn.start', request))
		await awaitingTool
		const call = events.find(event => event.type === 'tool.call.batch')?.tool_calls?.[0]
		assert.equal(call?.name, 'workspace.shell')
		await runtime.handle(envelope('exchange-2', 'turn.resume', {
			...request,
			inputs: [{ kind: 'tool.result', tool_call_id: call?.id, content: root }],
		}))
		await terminal
		assert.equal(events.at(-1)?.type, 'turn.completed', JSON.stringify(events))
		assert.equal(events.filter(event => event.type === 'assistant.delta').map(event => event.text).join(''), 'Pi bridge works')
		assert.equal(requestCount, 2)
  } finally {
    await runtime.shutdown()
    await new Promise<void>((resolve, reject) => server.close(error => error === undefined ? resolve() : reject(error)))
    for (const [name, value] of previous) {
      if (value === undefined) delete process.env[name]
      else process.env[name] = value
    }
  }
})

test('Pi runtime waits for cancellation before accepting the next turn', { timeout: 20_000 }, async () => {
  const root = await mkdtemp(join(tmpdir(), 'open-warp-pi-cancel-'))
  let requestCount = 0
  const server = createServer((_request, response) => {
    requestCount++
    response.writeHead(200, { 'content-type': 'text/event-stream' })
    if (requestCount === 1) {
      response.write(`data: ${JSON.stringify({
        id: 'chatcmpl-wait', object: 'chat.completion.chunk', created: 1, model: 'test-model',
        choices: [{ index: 0, delta: {
          role: 'assistant',
          tool_calls: [{ index: 0, id: 'cancel-call', type: 'function', function: { name: 'bash', arguments: '{"command":"sleep 30","execution_mode":"foreground"}' } }],
        }, finish_reason: null }],
      })}\n\n`)
      response.write(`data: ${JSON.stringify({
        id: 'chatcmpl-wait', object: 'chat.completion.chunk', created: 1, model: 'test-model',
        choices: [{ index: 0, delta: {}, finish_reason: 'tool_calls' }],
        usage: { prompt_tokens: 10, completion_tokens: 3, total_tokens: 13 },
      })}\n\n`)
      response.end('data: [DONE]\n\n')
      return
    }
    response.write(`data: ${JSON.stringify({
      id: 'chatcmpl-next', object: 'chat.completion.chunk', created: 1, model: 'test-model',
      choices: [{ index: 0, delta: { role: 'assistant', content: 'next turn works' }, finish_reason: null }],
    })}\n\n`)
    response.write(`data: ${JSON.stringify({
      id: 'chatcmpl-next', object: 'chat.completion.chunk', created: 1, model: 'test-model',
      choices: [{ index: 0, delta: {}, finish_reason: 'stop' }],
      usage: { prompt_tokens: 10, completion_tokens: 3, total_tokens: 13 },
    })}\n\n`)
    response.end('data: [DONE]\n\n')
  })
  await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve))
  const address = server.address()
  if (address === null || typeof address === 'string') throw new Error('fake provider did not bind a TCP port')

  const previous = new Map<string, string | undefined>()
  const env = {
    AGENT_RUNTIME_API_KEY: 'test-key', AGENT_RUNTIME_BASE_URL: `http://127.0.0.1:${address.port}/v1`,
    AGENT_RUNTIME_MODEL: 'test-model', AGENT_RUNTIME_THINKING_DISABLED: 'true',
    PI_AGENT_DIR: join(root, 'agent'), PI_SESSION_ROOT: join(root, 'sessions'),
  }
  for (const [name, value] of Object.entries(env)) {
    previous.set(name, process.env[name])
    process.env[name] = value
  }

  const events: Array<{ exchangeId: string; event: RuntimeEvent }> = []
  let toolReady: (() => void) | undefined
  let nextDone: (() => void) | undefined
  const awaitingTool = new Promise<void>(resolve => { toolReady = resolve })
  const nextCompleted = new Promise<void>(resolve => { nextDone = resolve })
  const runtime = new PiAgentRuntime((exchangeId, event) => {
    events.push({ exchangeId, event })
    if (event.type === 'turn.awaiting_tool') toolReady?.()
    if (exchangeId === 'exchange-next' && event.type === 'turn.completed') nextDone?.()
  })
  const request: TurnRequest = {
    conversation_id: 'cancel-conversation', task_id: 'cancel-task', request_id: 'request-1',
    working_dir: root, inputs: [{ kind: 'user.message', content: 'run a tool' }],
  }

  try {
    await runtime.handle(envelope('exchange-start', 'turn.start', request))
    await awaitingTool
    await runtime.handle(envelope('exchange-cancel', 'turn.cancel', {
      conversation_id: request.conversation_id, task_id: request.task_id,
    }))
    assert.equal(events.some(item => item.exchangeId === 'exchange-cancel' && item.event.type === 'turn.cancelled'), true)
    await runtime.handle(envelope('exchange-next', 'turn.start', {
      ...request, request_id: 'request-2', inputs: [{ kind: 'user.message', content: 'continue safely' }],
    }))
    await nextCompleted
    assert.equal(events.filter(item => item.exchangeId === 'exchange-next' && item.event.type === 'assistant.delta').map(item => item.event.text).join(''), 'next turn works')
    assert.equal(requestCount, 2)
  } finally {
    await runtime.shutdown()
    await new Promise<void>((resolve, reject) => server.close(error => error === undefined ? resolve() : reject(error)))
    for (const [name, value] of previous) {
      if (value === undefined) delete process.env[name]
      else process.env[name] = value
    }
  }
})
