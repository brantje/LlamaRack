import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

const source = readFileSync(resolve(process.cwd(), 'app/pages/logs/index.vue'), 'utf8')

describe('Request logs semantic cleanup', () => {
  it('uses shared semantic status notes for scoped errors and retained-session feedback', () => {
    expect(source).toContain('data-testid="request-log-error"')
    expect(source).toContain('<StatusTag variant="failed">Request history unavailable</StatusTag>')
    expect(source).toContain('data-testid="request-session-error"')
    expect(source).toContain('<StatusTag variant="failed">Session unavailable</StatusTag>')
    expect(source).toContain('data-testid="request-session-truncated"')
    expect(source).toContain('<StatusTag variant="pending">Session truncated</StatusTag>')
    expect(source).toContain('data-testid="request-detail-error"')
    expect(source).toContain('<StatusTag variant="failed">Request details unavailable</StatusTag>')
    expect(source).toContain('data-testid="request-failure-banner"')
    expect(source).toContain('<StatusTag variant="failed">Request failed</StatusTag>')
    expect(source).not.toContain('border-[var(--accent-800)]')
  })

  it('preserves the request/session/trace behavior hooks and bounded loading contract', () => {
    expect(source).toContain("const pageSize = 25")
    expect(source).toContain("const sessionPageSize = 100")
    expect(source).toContain("query.set('trace_id', traceID.value)")
    expect(source).toContain("query.request_id = item.request_id")
    expect(source).toContain("query.session_id = selection.sessionID")
    expect(source).toContain('liveRequestFingerprint')
    expect(source).toContain('Content not recorded')
    expect(source).toContain("detailMode === 'pretty'")
    expect(source).toContain("detailMode === 'json'")
  })
})
