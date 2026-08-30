import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const source = readFileSync(resolve(process.cwd(), 'app/pages/logs/index.vue'), 'utf8')

describe('request logs redesign hierarchy', () => {
  it('uses flat top-level request-history surfaces and semantic statuses', () => {
    expect(source).toContain('data-testid="request-logs-page"')
    expect(source).toContain('data-testid="request-logs-live-state"')
    expect(source).toContain('data-testid="trace-filter"')
    expect(source).toContain('data-testid="request-log-filters"')
    expect(source).toContain('data-testid="request-log-table"')
    expect(source).toContain('<StatusTag')
    expect(source).toContain('<Frame')
    expect(source).not.toContain('<UCard')
    expect(source).not.toContain('<UBadge')
    expect(source).not.toMatch(/rounded-(?:sm|md|lg|xl|2xl|3xl|full)/)
    expect(source).not.toMatch(/gradient|backdrop-blur/)
  })

  it('keeps the request inspector as one divider-based rail and detail pane', () => {
    expect(source).toContain('data-testid="request-detail-slideover"')
    expect(source).toContain('data-testid="request-sidebar"')
    expect(source).toContain('data-testid="request-detail-overview"')
    expect(source).toContain('data-testid="request-detail-metrics"')
    expect(source).toContain('data-testid="request-detail-content"')
    expect(source).toContain('data-testid="request-detail-client-metadata"')
    expect(source).toContain('Content not recorded')
    expect(source).toContain('Pretty')
    expect(source).toContain('JSON')
  })

  it('preserves bounded history, deep links and live refresh behavior', () => {
    expect(source).toContain("const pageSize = 25")
    expect(source).toContain("query.set('trace_id', traceID.value)")
    expect(source).toContain("query.request_id = item.request_id")
    expect(source).toContain("query.session_id = selection.sessionID")
    expect(source).toContain('/api/v1/observability/requests')
    expect(source).toContain('liveRequestFingerprint')
    expect(source).toContain('toggleLiveStreaming')
    expect(source).toContain('previousPage')
    expect(source).toContain('nextPage')
  })
})
