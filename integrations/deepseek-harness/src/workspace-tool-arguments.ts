export function workspaceToolArguments(dshName: string, raw: unknown): unknown {
  if ((dshName === 'bash_output' || dshName === 'bash_write' || dshName === 'bash_cancel') &&
    raw !== null && typeof raw === 'object' && !Array.isArray(raw)) {
    const { command_id: commandId, ...rest } = raw as Record<string, unknown>
    return { ...rest, commandId }
  }
  if (dshName !== 'bash' || raw === null || typeof raw !== 'object' || Array.isArray(raw)) return raw
  const args = raw as Record<string, unknown>
  const { run_in_background: runInBackground, ...rest } = args
  return {
    ...rest,
    executionMode: runInBackground === true
      ? 'background'
      : runInBackground === false
        ? 'foreground'
        : 'auto',
  }
}
