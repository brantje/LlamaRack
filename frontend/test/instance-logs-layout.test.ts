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

  it('removes card chrome only for embedded viewers', () => {
    expect(viewerSource).toContain('embedded?: boolean')
    expect(viewerSource).toContain("root: 'rounded-none bg-transparent shadow-none ring-0 divide-y-0'")
    expect(viewerSource).toContain("header: 'px-0 pt-0 pb-4 sm:px-0'")
    expect(viewerSource).toContain("body: 'p-0 sm:p-0'")
  })
})
