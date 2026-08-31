export function workspaceToolArguments(dshName: string, raw: unknown): unknown {
  if ((dshName === 'bash_output' || dshName === 'bash_write' || dshName === 'bash_cancel') &&
    raw !== null && typeof raw === 'object' && !Array.isArray(raw)) {
    const { command_id: commandId, ...rest } = raw as Record<string, unknown>
    return { ...rest, commandId }
  }
  if (dshName !== 'bash' || raw === null || typeof raw !== 'object' || Array.isArray(raw)) return raw
  const args = raw as Record<string, unknown>
  const { run_in_background: runInBackground, ...rest } = args
  if (typeof runInBackground !== 'boolean') {
    throw new Error('bash requires run_in_background to explicitly select foreground or background execution')
  }
  const command = typeof args.command === 'string' ? args.command : ''
  if (!runInBackground && (/\b(?:nohup|disown)\b/.test(command) || /(?:^|[;&|])\s*[^\n]*&(?:\s|$)/.test(command))) {
    throw new Error('foreground bash must not start a detached/background process; remove nohup, &, and disown, then set run_in_background=true')
  }
  return {
    ...rest,
    executionMode: runInBackground ? 'background' : 'foreground',
  }
}
