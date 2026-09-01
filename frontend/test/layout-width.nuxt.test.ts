import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

describe('default layout content width', () => {
  it('keeps responsive page gutters without constraining the dashboard body width', () => {
    const layout = readFileSync(resolve(process.cwd(), 'app/layouts/default.vue'), 'utf8')
    const body = layout.match(/<template #body>([\s\S]*?)<\/template>/)?.[1] ?? ''

    expect(body).toContain('class="min-w-0 w-full p-4 sm:p-6 lg:p-10"')
    expect(body).not.toContain('max-w-')
    expect(body).not.toContain('mx-auto')
  })

  it('keeps Nuxt UI dashboard chrome in charge of mobile sidebar layout', () => {
    const layout = readFileSync(resolve(process.cwd(), 'app/layouts/default.vue'), 'utf8')
    const appConfig = readFileSync(resolve(process.cwd(), 'app/app.config.ts'), 'utf8')

    expect(layout).toContain('<UDashboardGroup v-show="initialized && !backendError && !!user">')
    expect(layout).not.toContain('relative inset-auto min-h-svh overflow-visible')
    expect(layout).toContain('<UDashboardPanel id="manager-main" class="min-w-0"')
    expect(layout).toContain('<UDashboardNavbar title="llama.cpp" class="lg:hidden min-w-0" />')
    expect(layout).not.toContain('<UDashboardSidebarToggle />')
    expect(appConfig).toContain("root: 'w-full basis-full border-0 py-0 sm:basis-auto'")
    expect(appConfig).toContain('dashboardGroup:')
    expect(appConfig).not.toContain('overflow-hidden')
  })
})
