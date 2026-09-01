import { describe, expect, it } from 'vitest'
import { formatBackoffWait, startupBackoffMessage } from '~/utils/startupBackoff'

describe('startupBackoffMessage', () => {
  const now = Date.parse('2026-09-01T12:00:00.000Z')

  it('returns empty when retry_after is missing or elapsed', () => {
    expect(startupBackoffMessage(undefined, now)).toBe('')
    expect(startupBackoffMessage({ last_error: 'boom' }, now)).toBe('')
    expect(startupBackoffMessage({ retry_after: '2026-09-01T11:59:00.000Z' }, now)).toBe('')
  })

  it('formats remaining wait and includes last error', () => {
    expect(formatBackoffWait(45_000)).toBe('45s')
    expect(formatBackoffWait(60_000)).toBe('1m')
    expect(startupBackoffMessage({
      last_error: 'CUDA allocation failed',
      consecutive_start_failures: 2,
      retry_after: '2026-09-01T12:00:45.000Z'
    }, now)).toBe('CUDA allocation failed · Retry in 45s (2 consecutive start failures)')
    expect(startupBackoffMessage({
      consecutive_start_failures: 1,
      retry_after: '2026-09-01T12:00:15.000Z'
    }, now)).toBe('Retry in 15s (1 consecutive start failure)')
  })
})
