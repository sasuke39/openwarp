import { randomUUID } from 'node:crypto'
import { defineTool, type ToolDefinition } from '@earendil-works/pi-coding-agent'
import { Type } from 'typebox'
import type { RuntimeEvent } from './protocol.js'

export interface ToolOwner {
  conversationId: string
  workingDir: string
  active: boolean
  emit(event: RuntimeEvent): void
}

interface ExternalCall {
  id: string
  name: string
  arguments: unknown
}

interface PendingCall {
  owner: ToolOwner
  call: ExternalCall
  resolve(content: string): void
  reject(error: Error): void
  cleanup(): void
}

export class WorkspaceToolBroker {
  private readonly pending = new Map<string, PendingCall>()
  private readonly queues = new Map<ToolOwner, ExternalCall[]>()
  private readonly scheduled = new Set<ToolOwner>()

  execute(owner: ToolOwner, name: string, args: unknown, signal?: AbortSignal): Promise<string> {
    const call = { id: randomUUID(), name, arguments: args }
    return new Promise<string>((resolve, reject) => {
      const abort = (): void => {
        const pending = this.pending.get(call.id)
        if (pending === undefined) return
        this.pending.delete(call.id)
        pending.cleanup()
        reject(new Error('external workspace tool call aborted'))
      }
      const cleanup = (): void => signal?.removeEventListener('abort', abort)
      this.pending.set(call.id, { owner, call, resolve, reject, cleanup })
      const queue = this.queues.get(owner) ?? []
      queue.push(call)
      this.queues.set(owner, queue)
      this.schedule(owner)
      if (signal?.aborted === true) abort()
      else signal?.addEventListener('abort', abort, { once: true })
    })
  }

  deliver(id: string, content: string, isError = false): boolean {
    const pending = this.pending.get(id)
    if (pending === undefined) return false
    this.pending.delete(id)
    pending.cleanup()
    if (isError) pending.reject(new Error(content))
    else pending.resolve(content)
    return true
  }

  cancel(owner: ToolOwner, reason: string): void {
    this.queues.delete(owner)
    this.scheduled.delete(owner)
    for (const [id, pending] of this.pending) {
      if (pending.owner !== owner) continue
      this.pending.delete(id)
      pending.cleanup()
      pending.reject(new Error(reason))
    }
  }

  private schedule(owner: ToolOwner): void {
    if (this.scheduled.has(owner)) return
    this.scheduled.add(owner)
    queueMicrotask(() => {
      this.scheduled.delete(owner)
      const calls = (this.queues.get(owner) ?? []).filter(call => this.pending.has(call.id))
      this.queues.delete(owner)
      if (calls.length === 0) return
      if (!owner.active) {
        this.cancel(owner, 'Pi session is no longer active')
        return
      }
      owner.emit({ type: 'tool.call.batch', tool_calls: calls })
      owner.emit({ type: 'turn.awaiting_tool' })
    })
  }
}

function result(content: string) {
  return { content: [{ type: 'text' as const, text: content }], details: {} }
}

export function createWorkspaceTools(owner: ToolOwner, broker: WorkspaceToolBroker): ToolDefinition[] {
  const run = (name: string, args: unknown, signal?: AbortSignal) => broker.execute(owner, name, args, signal)
  return [
    defineTool({
      name: 'bash', label: 'Bash', promptSnippet: 'Execute shell commands in the active Warp terminal',
      description: 'Execute a shell command in the active Warp terminal, which may be local or SSH. Use background for servers and other commands that are expected to keep running; use foreground only when the command must finish before continuing.',
      parameters: Type.Object({
        command: Type.String(),
        timeout: Type.Optional(Type.Number()),
        execution_mode: Type.Optional(Type.Union([
          Type.Literal('auto'),
          Type.Literal('foreground'),
          Type.Literal('background'),
        ])),
      }),
      execute: async (_id, args, signal) => result(await run('workspace.shell', {
        command: args.command,
        workdir: owner.workingDir,
        timeoutMs: args.timeout,
        executionMode: args.execution_mode ?? 'auto',
      }, signal)),
    }),
    defineTool({
      name: 'read', label: 'Read', promptSnippet: 'Read file contents from the active Warp terminal',
      description: 'Read a UTF-8 file, optionally selecting a line range.',
      parameters: Type.Object({ path: Type.String(), offset: Type.Optional(Type.Number()), limit: Type.Optional(Type.Number()) }),
      execute: async (_id, args, signal) => result(await run('workspace.read_file', {
        file_path: args.path, offset: args.offset, limit: args.limit,
      }, signal)),
    }),
    defineTool({
      name: 'write', label: 'Write', promptSnippet: 'Create or overwrite files in the active Warp terminal',
      description: 'Create or completely replace a UTF-8 file.',
      parameters: Type.Object({ path: Type.String(), content: Type.String() }),
      execute: async (_id, args, signal) => result(await run('workspace.write_file', {
        file_path: args.path, content: args.content,
      }, signal)),
    }),
    defineTool({
      name: 'edit', label: 'Edit', promptSnippet: 'Make exact text replacements in files',
      promptGuidelines: ['Each edits[].oldText must match exactly and edits must not overlap.'],
      description: 'Apply one or more exact, non-overlapping text replacements to a file.',
      parameters: Type.Object({
        path: Type.String(),
        edits: Type.Array(Type.Object({ oldText: Type.String(), newText: Type.String() }), { minItems: 1 }),
      }),
      executionMode: 'sequential',
      execute: async (_id, args, signal) => {
        const outputs = await Promise.all(args.edits.map(edit => run('workspace.edit_file', {
          file_path: args.path, old_string: edit.oldText, new_string: edit.newText,
        }, signal)))
        return result(outputs.join('\n'))
      },
    }),
    defineTool({
      name: 'grep', label: 'Grep', promptSnippet: 'Search file contents in the active Warp terminal',
      description: 'Search file contents for a pattern.',
      parameters: Type.Object({
        pattern: Type.String(), path: Type.Optional(Type.String()), glob: Type.Optional(Type.String()),
        ignoreCase: Type.Optional(Type.Boolean()), literal: Type.Optional(Type.Boolean()),
        context: Type.Optional(Type.Number()), limit: Type.Optional(Type.Number()),
      }),
      execute: async (_id, args, signal) => result(await run('workspace.grep', args, signal)),
    }),
    defineTool({
      name: 'find', label: 'Find', promptSnippet: 'Find paths by glob in the active Warp terminal',
      description: 'Find paths matching a glob pattern.',
      parameters: Type.Object({ pattern: Type.String(), path: Type.Optional(Type.String()), limit: Type.Optional(Type.Number()) }),
      execute: async (_id, args, signal) => result(await run('workspace.glob', args, signal)),
    }),
    defineTool({
      name: 'ls', label: 'List', promptSnippet: 'List directory contents in the active Warp terminal',
      description: 'List a directory, including hidden entries and metadata.',
      parameters: Type.Object({ path: Type.Optional(Type.String()), limit: Type.Optional(Type.Number()) }),
      execute: async (_id, args, signal) => result(await run('workspace.shell', {
        command: `ls -la -- ${shellQuote(args.path ?? '.')}`, workdir: owner.workingDir,
      }, signal)),
    }),
  ]
}

function shellQuote(value: string): string {
  return `'${value.replaceAll("'", `'"'"'`)}'`
}
