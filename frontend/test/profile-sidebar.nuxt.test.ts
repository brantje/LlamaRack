import { describe, expect, it } from 'vitest'
import { mountSuspended } from '@nuxt/test-utils/runtime'
import UserSidebar from '~/components/navigation/UserSidebar.vue'

describe('UserSidebar', () => {
  it('renders account navigation links for desktop and mobile', async () => {
    const sidebar = await mountSuspended(UserSidebar, { route: '/profile/account' })
    expect(sidebar.get('[data-testid="profile-secondary-nav"]').classes()).toContain('lg:w-[216px]')
    expect(sidebar.text()).toContain('Account')
    expect(sidebar.text()).toContain('Authentication')
    expect(sidebar.text()).toContain('Sessions')
    expect(sidebar.findAll('a').some(link => link.attributes('href') === '/profile/account')).toBe(true)
    expect(sidebar.findAll('a').some(link => link.attributes('href') === '/profile/authentication')).toBe(true)
    expect(sidebar.findAll('a').some(link => link.attributes('href') === '/profile/sessions')).toBe(true)
    sidebar.unmount()
  })
})
