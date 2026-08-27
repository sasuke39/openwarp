export function workspaceToolArguments(dshName: string, raw: unknown): unknown {
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
