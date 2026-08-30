import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const source = (path: string) => readFileSync(resolve(process.cwd(), path), 'utf8')

describe('Administration machine typography', () => {
  it('keeps llama.cpp numeric capabilities mono and tabular', () => {
    const content = source('app/pages/admin/llamacpp.vue')
    expect(content).toContain('font-mono text-[12.5px] tabular-nums">{{ profile.options.length }}')
  })

  it('keeps System machine values mono and numeric values tabular', () => {
    const content = source('app/pages/admin/system.vue')
    expect(content).toContain('font-mono text-[12.5px] tabular-nums">{{ info.manager.uptime_seconds }} seconds')
    expect(content).toContain('font-mono text-[12.5px]">{{ info.llamacpp.version || \'unknown\' }}')
    expect(content).toContain('font-mono text-[12.5px] tabular-nums">{{ info.llamacpp.options || 0 }}')
  })

  it('keeps user timestamps mono and tabular', () => {
    const content = source('app/pages/admin/users.vue')
    expect(content.match(/font-mono text-xs tabular-nums text-\[var\(--neutral-700\)\]/g)).toHaveLength(2)
  })
})
