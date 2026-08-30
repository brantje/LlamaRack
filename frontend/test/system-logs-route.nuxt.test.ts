import { describe, expect, it } from 'vitest'
import { mountSuspended } from '@nuxt/test-utils/runtime'
import AdminSidebar from '~/components/navigation/AdminSidebar.vue'
import SystemLogsPage from '~/pages/admin/system-logs.vue'

describe('Administration system logs route', () => {
  it('uses /admin/system-logs as the canonical Administration destination', async () => {
    expect(SystemLogsPage).toBeTruthy()

    const wrapper = await mountSuspended(AdminSidebar, { route: '/admin/system-logs' })
    const logs = wrapper.get('[data-testid="admin-nav-logs"]')

    expect(logs.attributes('href')).toBe('/admin/system-logs')
    expect(logs.classes()).toContain('border-[var(--color-accent)]')
    wrapper.unmount()
  })
})
