import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

describe('Administration Logs accessibility contract', () => {
  it('uses Nuxt UI buttons for log filters and readable metadata tokens', () => {
    const source = readFileSync(resolve(process.cwd(), 'app/pages/admin/system-logs.vue'), 'utf8')
    expect(source).toContain('<UButton\n            v-for="level in levels"')
    expect(source).toContain('<UButton\n          v-for="source in sourceItems"')
    expect(source).toContain("intent=\"ghost\" size=\"xs\" class=\"ml-2\"")
    expect(source).toContain("if (level === 'DEBUG') return 'text-[var(--neutral-700)]'")
    expect(source).toContain('class="whitespace-nowrap text-[var(--neutral-700)]"')
    expect(source).toContain('class="truncate text-[var(--neutral-800)]"')
    expect(source).not.toContain('opacity-45')
    expect(source).not.toContain('opacity-55')
  })
})
