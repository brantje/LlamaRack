export type StartupBackoffRuntime = {
  last_error?: string
  consecutive_start_failures?: number
  retry_after?: string
}

export function formatBackoffWait(ms: number): string {
  const seconds = Math.max(1, Math.ceil(ms / 1000))
  if (seconds >= 60) return `${Math.ceil(seconds / 60)}m`
  return `${seconds}s`
}

export function startupBackoffMessage(runtime?: StartupBackoffRuntime | null, now = Date.now()): string {
  if (!runtime?.retry_after) return ''
  const retryAfter = Date.parse(runtime.retry_after)
  if (!Number.isFinite(retryAfter) || retryAfter <= now) return ''
  const wait = formatBackoffWait(retryAfter - now)
  const failures = runtime.consecutive_start_failures || 0
  const noun = failures === 1 ? 'failure' : 'failures'
  const prefix = `Retry in ${wait} (${failures} consecutive start ${noun})`
  return runtime.last_error ? `${runtime.last_error} · ${prefix}` : prefix
}
