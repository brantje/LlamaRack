import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

describe('Instances control accessibility contract', () => {
  it('uses Nuxt UI buttons and pressed semantics for view and state controls', () => {
    const source = readFileSync(resolve(process.cwd(), 'app/pages/instances/index.vue'), 'utf8')
    expect(source).toContain(`data-testid="instances-view-table" :aria-pressed="viewMode === 'table'"`)
    expect(source).toContain(`data-testid="instances-view-cards" :aria-pressed="viewMode === 'cards'"`)
    expect(source).toContain(':aria-pressed="stateFilter === filter.value"')
    expect(source).toContain(":color=\"stateFilter === filter.value ? 'primary' : 'neutral'\"")
    expect(source).toContain(":variant=\"stateFilter === filter.value ? 'soft' : 'ghost'\"")
    expect(source).not.toContain('class="border px-3 py-1.5 text-xs font-semibold transition-colors"')
  })
})
