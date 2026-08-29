import { DEFAULT_THEME_ID, THEME_STORAGE_KEY, resolveThemeId, themeMeta, type ThemeId } from '~/themes'

export function useAppTheme() {
  const currentTheme = useState<ThemeId>('app-theme', () => DEFAULT_THEME_ID)
  const initialized = useState<boolean>('app-theme-initialized', () => false)

  function applyTheme(id: ThemeId) {
    currentTheme.value = id
    if (!import.meta.client) return
    const metadata = themeMeta(id)
    document.documentElement.dataset.theme = id
    document.documentElement.style.colorScheme = metadata.colorScheme
  }

  function initializeTheme() {
    if (!import.meta.client || initialized.value) return
    const id = resolveThemeId(localStorage.getItem(THEME_STORAGE_KEY))
    applyTheme(id)
    initialized.value = true
  }

  function setTheme(value: ThemeId | string) {
    const id = resolveThemeId(value)
    applyTheme(id)
    if (import.meta.client) localStorage.setItem(THEME_STORAGE_KEY, id)
  }

  return { currentTheme, initialized, initializeTheme, setTheme }
}
