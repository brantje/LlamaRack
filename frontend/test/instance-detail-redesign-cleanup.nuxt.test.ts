import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const detailSource = readFileSync(resolve(process.cwd(), 'app/pages/instances/[id]/detail.vue'), 'utf8')

describe('Instance detail redesign cleanup', () => {
  it('uses the semantic error note instead of UAlert', () => {
    expect(detailSource).toContain('data-testid="instance-detail-error"')
    expect(detailSource).toContain('<StatusTag variant="failed">Instance detail unavailable</StatusTag>')
    expect(detailSource).not.toContain('<UAlert')
  })

  it('retains scoped history and the required operational surfaces', () => {
    expect(detailSource).toContain('instance_id: instanceID.value')
    expect(detailSource).toContain('data-testid="instance-detail-chart-requests"')
    expect(detailSource).toContain('data-testid="instance-detail-chart-tokens"')
    expect(detailSource).toContain('data-testid="instance-detail-chart-latency"')
    expect(detailSource).toContain('data-testid="instance-detail-chart-context"')
    expect(detailSource).toContain('(this Instance)')
  })
})
