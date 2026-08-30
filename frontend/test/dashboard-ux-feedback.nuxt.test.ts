import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

describe('dashboard UX feedback foundations', () => {
  it('keeps dashboard naming and mobile control targets consistent', () => {
    const sidebar = readFileSync(resolve(process.cwd(), 'app/components/navigation/AppSidebar.vue'), 'utf8')
    const appConfig = readFileSync(resolve(process.cwd(), 'app/app.config.ts'), 'utf8')
    const shared = readFileSync(resolve(process.cwd(), 'app/themes/shared.css'), 'utf8')

    expect(sidebar).toContain("{ label: 'Dashboard', icon: 'i-lucide-layout-dashboard', to: '/' }")
    expect(sidebar).not.toContain("{ label: 'Overview', icon: 'i-lucide-layout-dashboard', to: '/' }")
    expect(appConfig).toContain('max-lg:min-h-11 max-lg:min-w-11')
    expect(appConfig).toContain("base: 'rounded-none max-lg:min-h-11'")
    expect(appConfig).toContain("headline: 'text-xs")
    expect(shared).toContain('--font-size-table-header: 0.75rem')
  })
})
