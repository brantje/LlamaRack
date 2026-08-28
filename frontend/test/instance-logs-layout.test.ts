import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const source = readFileSync(fileURLToPath(new URL('../app/pages/instances/index.vue', import.meta.url)), 'utf8')

describe('Instance logs layout', () => {
  it('keeps the logs dialog console-sized and mounts the shared live viewer', () => {
    expect(source).toContain("content: 'w-[calc(100vw-2rem)] max-w-none sm:max-w-6xl'")
    expect(source).toContain('<InstanceLogViewer v-if="logsOpen && logInstanceId" :instance-id="logInstanceId" />')
    expect(source).not.toContain('/logs`)')
    expect(source).not.toContain('instance-logs-output')
  })
})
