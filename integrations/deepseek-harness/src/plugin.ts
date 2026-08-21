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
      description: 'Execute a command in the active Warp terminal. This may be a local or SSH terminal.',
      parameters: {
        command: { type: 'string', required: true },
        description: { type: 'string', required: true },
        workdir: { type: 'string' },
        timeoutMs: { type: 'number' },
        run_in_background: { type: 'boolean' },
      },
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
