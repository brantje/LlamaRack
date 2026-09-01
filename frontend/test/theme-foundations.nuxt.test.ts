import { beforeEach, describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { nextTick } from 'vue'
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

  it('applies the stored theme before first paint via head script and a client plugin', () => {
    const app = readFileSync(resolve(process.cwd(), 'app/app.vue'), 'utf8')
    const plugin = readFileSync(resolve(process.cwd(), 'app/plugins/theme.client.ts'), 'utf8')
    const config = readFileSync(resolve(process.cwd(), 'nuxt.config.ts'), 'utf8')
    expect(app).not.toContain('onMounted(initializeTheme)')
    expect(plugin).toContain('initializeTheme()')
    expect(config).toContain('themeBootstrap')
    expect(config).toContain("script: [{ innerHTML: themeBootstrap }]")
    expect(config).toContain("const key = 'llamarack-theme'")
    expect(config).not.toContain('llamacpp-manager-theme')
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

  it('keeps the applied theme when localStorage persistence fails', () => {
    const theme = useAppTheme()
    theme.initializeTheme()
    const originalSetItem = Storage.prototype.setItem
    Storage.prototype.setItem = () => {
      throw new Error('quota')
    }
    try {
      expect(() => theme.setTheme('light')).not.toThrow()
      expect(theme.currentTheme.value).toBe('light')
      expect(document.documentElement.dataset.theme).toBe('light')
    } finally {
      Storage.prototype.setItem = originalSetItem
    }
  })

  it('falls back to the default theme when localStorage reads fail', () => {
    const originalGetItem = Storage.prototype.getItem
    Storage.prototype.getItem = () => {
      throw new Error('blocked')
    }
    try {
      const theme = useAppTheme()
      expect(() => theme.initializeTheme()).not.toThrow()
      expect(theme.currentTheme.value).toBe('dark')
      expect(document.documentElement.dataset.theme).toBe('dark')
    } finally {
      Storage.prototype.getItem = originalGetItem
    }
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
    expect(frame.find('[data-testid="frame-collapse-toggle"]').exists()).toBe(false)
    expect(frame.element.querySelectorAll(':scope > .flex.w-full')).toHaveLength(0)
  })

  it('does not inject a full-width collapse row that would shove flex error notes off-screen', async () => {
    const frame = await mountSuspended(Frame, {
      route: false,
      attrs: { class: 'flex items-start gap-2 p-3' },
      slots: { default: '<span>Request error</span><p>gateway rejected the prompt</p>' }
    })
    expect(frame.element.querySelectorAll(':scope > .flex.w-full')).toHaveLength(0)
    expect(frame.text()).toContain('gateway rejected the prompt')
  })

  it('hides collapsible frame content by default and toggles from the top-right control', async () => {
    const frame = await mountSuspended(Frame, {
      route: false,
      props: { collapsible: true },
      slots: { default: 'collapsible content' }
    })

    expect(frame.find('[data-testid="frame-collapse-toggle"]').exists()).toBe(true)
    expect(frame.element.querySelectorAll(':scope > .flex.w-full')).toHaveLength(1)
    expect(frame.find('[data-testid="frame-collapse-toggle"]').attributes('aria-expanded')).toBe('false')
    expect(frame.text()).not.toContain('collapsible content')

    await frame.find('[data-testid="frame-collapse-toggle"]').trigger('click')
    await nextTick()
    expect(frame.find('[data-testid="frame-collapse-toggle"]').attributes('aria-expanded')).toBe('true')
    expect(frame.text()).toContain('collapsible content')

    await frame.find('[data-testid="frame-collapse-toggle"]').trigger('click')
    await nextTick()
    expect(frame.find('[data-testid="frame-collapse-toggle"]').attributes('aria-expanded')).toBe('false')
    expect(frame.text()).not.toContain('collapsible content')
  })

  it('supports controlled collapsible frame state', async () => {
    const frame = await mountSuspended(Frame, {
      route: false,
      props: { collapsible: true, open: false, 'onUpdate:open': (value: boolean) => frame.setProps({ open: value }) },
      slots: { default: 'controlled content' }
    })

    expect(frame.text()).not.toContain('controlled content')
    await frame.find('[data-testid="frame-collapse-toggle"]').trigger('click')
    await nextTick()
    expect(frame.props('open')).toBe(true)
    expect(frame.text()).toContain('controlled content')
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
