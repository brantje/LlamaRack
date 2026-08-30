import { beforeEach, describe, expect, it } from 'vitest'
import { mountSuspended } from '@nuxt/test-utils/runtime'
import { clearNuxtState } from '#app'
import AppButton from '~/components/AppButton.vue'
import Frame from '~/components/Frame.vue'
import StatusTag from '~/components/StatusTag.vue'
import ThemeSelector from '~/components/ThemeSelector.vue'
import { useAppTheme } from '~/composables/useAppTheme'
import { DEFAULT_THEME_ID, THEME_STORAGE_KEY, resolveThemeId, themeMeta, themes, type ThemeId } from '~/themes'

beforeEach(() => {
  localStorage.clear()
  clearNuxtState('app-theme')
  clearNuxtState('app-theme-initialized')
  document.documentElement.removeAttribute('data-theme')
  document.documentElement.style.colorScheme = ''
})

describe('redesign theme foundations', () => {
  it('registers Dark as the default and safely resolves registered and unknown ids', () => {
    expect(DEFAULT_THEME_ID).toBe('dark')
    expect(themes.map(theme => theme.id)).toEqual(['dark', 'light'])
    expect(resolveThemeId('dark')).toBe('dark')
    expect(resolveThemeId('light')).toBe('light')
    expect(resolveThemeId('removed-theme')).toBe('dark')
    expect(themeMeta('light').colorScheme).toBe('light')
    expect(themeMeta('removed-theme' as ThemeId).id).toBe('dark')
  })

  it('applies, persists and restores the selected theme with an unknown-id fallback', () => {
    const theme = useAppTheme()
    theme.initializeTheme()
    expect(theme.currentTheme.value).toBe('dark')
    expect(document.documentElement.dataset.theme).toBe('dark')
    expect(document.documentElement.style.colorScheme).toBe('dark')

    theme.setTheme('light')
    expect(theme.currentTheme.value).toBe('light')
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe('light')
    expect(document.documentElement.dataset.theme).toBe('light')
    expect(document.documentElement.style.colorScheme).toBe('light')

    theme.setTheme('not-registered')
    expect(theme.currentTheme.value).toBe('dark')
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe('dark')

    theme.initializeTheme()
    expect(theme.currentTheme.value).toBe('dark')
  })

  it('restores a valid saved choice and rejects a removed saved theme', () => {
    localStorage.setItem(THEME_STORAGE_KEY, 'light')
    let theme = useAppTheme()
    theme.initializeTheme()
    expect(theme.currentTheme.value).toBe('light')

    clearNuxtState('app-theme')
    clearNuxtState('app-theme-initialized')
    localStorage.setItem(THEME_STORAGE_KEY, 'future-theme')
    theme = useAppTheme()
    theme.initializeTheme()
    expect(theme.currentTheme.value).toBe('dark')
  })

  it('renders the global selector and shared flat frame', async () => {
    const selector = await mountSuspended(ThemeSelector, { route: false })
    expect(selector.find('[data-testid="theme-selector"]').exists()).toBe(true)
    expect(selector.find('[aria-label="Application theme"]').exists()).toBe(true)

    const frame = await mountSuspended(Frame, { route: false, slots: { default: 'content' } })
    expect(frame.text()).toContain('content')
    expect(frame.classes()).toContain('shadow-none')
    expect(frame.attributes('class')).toContain('var(--color-surface)')
  })

  it('renders every semantic status treatment', async () => {
    const wrapper = await mountSuspended(StatusTag, {
      route: false,
      props: { variant: 'ready' },
      slots: { default: 'READY' }
    })
    expect(wrapper.text()).toContain('READY')
    expect(wrapper.attributes('class')).toContain('accent-100')

    await wrapper.setProps({ variant: 'pending' })
    expect(wrapper.attributes('class')).toContain('color-accent')
    await wrapper.setProps({ variant: 'neutral' })
    expect(wrapper.attributes('class')).toContain('neutral-300')
    await wrapper.setProps({ variant: 'failed' })
    expect(wrapper.attributes('class')).toContain('accent-800')
  })

  it('renders the shared action hierarchy and forwards normal button attributes', async () => {
    for (const intent of ['primary', 'secondary', 'ghost'] as const) {
      const wrapper = await mountSuspended(AppButton, {
        route: false,
        props: { intent },
        attrs: { 'aria-label': `${intent} action` },
        slots: { default: intent }
      })
      expect(wrapper.text()).toContain(intent)
      expect(wrapper.find(`[aria-label="${intent} action"]`).exists()).toBe(true)
    }

    const destructiveSecondary = await mountSuspended(AppButton, {
      route: false,
      props: { intent: 'secondary', tone: 'destructive' },
      attrs: { 'aria-label': 'destructive secondary action' },
      slots: { default: 'Delete' }
    })
    expect(destructiveSecondary.attributes('class')).toContain('danger-700')

    const destructivePrimary = await mountSuspended(AppButton, {
      route: false,
      props: { intent: 'primary', tone: 'destructive' },
      attrs: { 'aria-label': 'destructive primary action' },
      slots: { default: 'Confirm delete' }
    })
    expect(destructivePrimary.attributes('class')).toContain('color-danger')
  })
})
