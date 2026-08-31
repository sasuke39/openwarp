import type { Context } from '@deepseek-ai/cordis'
import z from '@deepseek-ai/schemastery'
import { defineTool, type ParameterSchemaSpec } from '@deepseek-ai/dsh-tools'
import { ExternalToolBroker } from './socket-broker.js'

export const name = 'open-warp-external-tools'
export const inject = ['tools']

export interface Config {
  socketPath: string
}

export const Config: z<Config> = z.object({ socketPath: z.string().required() })

function tool(
  broker: ExternalToolBroker,
  definition: { name: string; description: string; parameters: ParameterSchemaSpec },
) {
  return defineTool({
    ...definition,
    output: {
      schema: {
        type: 'object',
        additionalProperties: false,
        properties: { content: { type: 'string', required: true } },
      },
      render: (_args, value) => [{ type: 'text', text: value.content }],
    },
    async execute(args, exec) {
      const sessionId = exec.agent?.session.id
      if (sessionId === undefined) throw new Error('external tool requires an agent session')
      const result = await broker.execute({
        id: String(exec.callId),
        session_id: String(sessionId),
        name: definition.name,
        arguments: args,
      }, exec.signal)
      if (result.is_error === true) throw new Error(result.content)
      return { content: result.content }
    },
  })
}

export function apply(ctx: Context, config: Config): void {
  const broker = new ExternalToolBroker(config.socketPath)
  ctx.effect(() => () => broker.close())

  const definitions: Array<{ name: string; description: string; parameters: ParameterSchemaSpec }> = [
    {
      name: 'bash',
      description: 'Execute a command in the active Warp terminal. Set run_in_background=false only when it should exit within about 10 seconds and its final result is needed immediately. Set it true when it may exceed 10 seconds, is persistent, has unpredictable duration, needs parallel observation, or the user requests asynchronous execution; this includes deploys, full builds/tests, servers, watchers, and log followers. Warp creates a managed command_id: submit only the original command, never add nohup, &, or disown, and poll bash_output until exited before claiming completion. If Warp reports unfinished managed commands, inspect/reuse/cancel them first; repeat the exact same background call only when intentionally forcing a second process. If a foreground command exceeds the observation window, do not run unrelated commands: use bash_output/bash_cancel, or repeat the original command with run_in_background=true to detach the same process without restarting it.',
      parameters: {
        command: { type: 'string', required: true },
        description: { type: 'string', required: true },
        workdir: { type: 'string' },
        timeoutMs: { type: 'number' },
        run_in_background: { type: 'boolean', required: true },
      },
    },
    {
      name: 'bash_output',
      description: 'Read captured output and status from a background bash command.',
      parameters: { command_id: { type: 'string', required: true } },
    },
    {
      name: 'bash_write',
      description: 'Write text to the standard input of a running background bash command.',
      parameters: {
        command_id: { type: 'string', required: true },
        input: { type: 'string', required: true },
      },
    },
    {
      name: 'bash_cancel',
      description: 'Terminate a background bash command and its child process group.',
      parameters: { command_id: { type: 'string', required: true } },
    },
    {
      name: 'read',
      description: 'Read a UTF-8 file from the active Warp terminal workspace.',
      parameters: {
        file_path: { type: 'string', required: true },
        offset: { type: 'integer' },
        limit: { type: 'integer' },
      },
    },
    {
      name: 'write',
      description: 'Create or replace a UTF-8 file in the active Warp terminal workspace.',
      parameters: {
        file_path: { type: 'string', required: true },
        content: { type: 'string', required: true },
      },
    },
    {
      name: 'edit',
      description: 'Replace literal text in a file in the active Warp terminal workspace.',
      parameters: {
        file_path: { type: 'string', required: true },
        old_string: { type: 'string', required: true },
        new_string: { type: 'string', required: true },
        replace_all: { type: 'boolean' },
      },
    },
    {
      name: 'glob',
      description: 'Find paths matching a glob in the active Warp terminal workspace.',
      parameters: {
        pattern: { type: 'string', required: true },
        path: { type: 'string' },
      },
    },
    {
      name: 'grep',
      description: 'Search file contents in the active Warp terminal workspace.',
      parameters: {
        pattern: { type: 'string', required: true },
        path: { type: 'string' },
        glob: { type: 'string' },
      },
    },
  ]
  for (const definition of definitions) ctx.tools.register(tool(broker, definition))
}
