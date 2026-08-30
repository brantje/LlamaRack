import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const dashboardSource = readFileSync(resolve(process.cwd(), 'app/pages/index.vue'), 'utf8')

describe('dashboard redesign cleanup', () => {
  it('uses the canonical system logs route', () => {
    expect(dashboardSource).toContain('to="/admin/system-logs"')
    expect(dashboardSource).not.toContain('to="/admin/logs"')
  })

  it('renders dashboard errors with the semantic frame and status tag pattern', () => {
    expect(dashboardSource).toContain('data-testid="dashboard-error"')
    expect(dashboardSource).toContain('<StatusTag variant="failed">Observability data unavailable</StatusTag>')
    expect(dashboardSource).not.toContain('<UAlert')
  })
})
