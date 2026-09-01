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

  function readStoredTheme() {
    if (!import.meta.client) return null
    try {
      return localStorage.getItem(THEME_STORAGE_KEY)
    } catch {
      return null
    }
  }

  function writeStoredTheme(id: ThemeId) {
    if (!import.meta.client) return
    try {
      localStorage.setItem(THEME_STORAGE_KEY, id)
    } catch {
      // Private mode and storage-quota failures must not undo the in-document theme.
    }
  }

  function initializeTheme() {
    if (!import.meta.client || initialized.value) return
    const id = resolveThemeId(readStoredTheme())
    applyTheme(id)
    initialized.value = true
  }

  function setTheme(value: ThemeId | string) {
    const id = resolveThemeId(value)
    applyTheme(id)
    writeStoredTheme(id)
  }

  return { currentTheme, initialized, initializeTheme, setTheme }
}
