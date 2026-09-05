import { resolveFrontendApiBase } from './config/public-api-base'

const themeBootstrap = `(() => {
  const key = 'llamarack-theme'
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
      apiBase: resolveFrontendApiBase(process.env)
    }
  },
  app: {
    head: {
      title: 'LlamaRack',
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
