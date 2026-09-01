const themeBootstrap = `(() => {
  const key = 'llamacpp-manager-theme'
  const allowed = new Set(['dark', 'light'])
  let value = 'dark'
  try {
    const stored = localStorage.getItem(key)
    if (stored && allowed.has(stored)) value = stored
  } catch {}
  document.documentElement.dataset.theme = value
  document.documentElement.style.colorScheme = value
})()`

export default defineNuxtConfig({
  ssr: false,
  modules: ['@nuxt/ui'],
  devtools: { enabled: process.env.CI !== 'true' },
  runtimeConfig: {
    public: {
      apiBase: process.env.NUXT_PUBLIC_API_BASE || ''
    }
  },
  app: {
    head: {
      title: 'llamacpp-manager',
      meta: [
        { name: 'viewport', content: 'width=device-width, initial-scale=1' },
        { name: 'color-scheme', content: 'dark light' }
      ],
      script: [{ innerHTML: themeBootstrap }]
    }
  },
  colorMode: {
    preference: 'dark',
    fallback: 'dark'
  },
  css: ['~/assets/css/main.css'],
  typescript: { strict: true }
})
