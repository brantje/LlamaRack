export const THEME_STORAGE_KEY = 'llamacpp-manager-theme'
export const DEFAULT_THEME_ID = 'dark'

export const themes = [
  { id: 'dark', label: 'Dark', colorScheme: 'dark' },
  { id: 'light', label: 'Light', colorScheme: 'light' }
] as const

export type ThemeId = typeof themes[number]['id']

export function resolveThemeId(value: unknown): ThemeId {
  return themes.some(theme => theme.id === value) ? value as ThemeId : DEFAULT_THEME_ID
}

export function themeMeta(id: ThemeId) {
  return themes.find(theme => theme.id === id) ?? themes[0]
}
