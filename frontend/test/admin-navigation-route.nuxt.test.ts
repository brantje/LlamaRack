import { describe, expect, it } from 'vitest'
import { mountSuspended } from '@nuxt/test-utils/runtime'
import AdminSidebar from '~/components/navigation/AdminSidebar.vue'

describe('Administration secondary navigation', () => {
  it('routes Logs to the aggregated system log viewer', async () => {
    const wrapper = await mountSuspended(AdminSidebar, { route: '/admin/system' })
    const logs = wrapper.get('[data-testid="admin-nav-logs"]')
    expect(logs.attributes('href')).toBe('/admin/system-logs')
    expect(logs.attributes('href')).not.toBe('/admin/logs')
    wrapper.unmount()
  })
})
