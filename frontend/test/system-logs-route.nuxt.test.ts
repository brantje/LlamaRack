import { existsSync, readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { mountSuspended } from '@nuxt/test-utils/runtime'
import AdminSidebar from '~/components/navigation/AdminSidebar.vue'
import SystemLogsPage from '~/pages/admin/system-logs.vue'

const adminPages = resolve(process.cwd(), 'app/pages/admin')

describe('Administration system logs route', () => {
  it('uses /admin/system-logs as the only Administration system logs page', async () => {
    expect(SystemLogsPage).toBeTruthy()
    expect(existsSync(resolve(adminPages, 'logs.vue'))).toBe(false)

    const source = readFileSync(resolve(adminPages, 'system-logs.vue'), 'utf8')
    expect(source).toContain('data-testid="system-log-viewer"')
    expect(source).not.toContain("'./logs.vue'")
    expect(source).not.toContain('"./logs.vue"')

    const wrapper = await mountSuspended(AdminSidebar, { route: '/admin/system-logs' })
    const logs = wrapper.get('[data-testid="admin-nav-logs"]')

    expect(logs.attributes('href')).toBe('/admin/system-logs')
    expect(logs.classes()).toContain('border-[var(--color-accent)]')
    wrapper.unmount()
  })
})
