import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const pageSource = readFileSync(fileURLToPath(new URL('../app/pages/instances/index.vue', import.meta.url)), 'utf8')
const viewerSource = readFileSync(fileURLToPath(new URL('../app/components/InstanceLogViewer.vue', import.meta.url)), 'utf8')

describe('Instance logs layout', () => {
  it('keeps the logs dialog console-sized and mounts the shared live viewer in flat embedded mode', () => {
    expect(pageSource).toContain("content: 'w-[calc(100vw-2rem)] max-w-none sm:max-w-6xl'")
    expect(pageSource).toContain('<InstanceLogViewer v-if="logsOpen && logInstanceId" :instance-id="logInstanceId" embedded />')
    expect(pageSource).not.toContain('/logs`)')
    expect(pageSource).not.toContain('instance-logs-output')
  })

  it('uses shared flat primitives and the Administration diagnostics route', () => {
    expect(viewerSource).toContain('embedded?: boolean')
    expect(viewerSource).toContain("path: '/admin/system-logs'")
    expect(viewerSource).toContain('<Frame')
    expect(viewerSource).toContain('<StatusTag')
    expect(viewerSource).toContain("props.embedded ? '!border-0 !bg-transparent !p-0' : 'p-4'")
    expect(viewerSource).not.toContain('<UCard')
    expect(viewerSource).not.toContain('<UBadge')
    expect(viewerSource).not.toContain('<UAlert')
    expect(viewerSource).not.toMatch(/rounded-(?:sm|md|lg|xl|2xl|3xl|full)/)
  })
})
