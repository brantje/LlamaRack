export default defineNuxtConfig({
  ssr: false,
  modules: ['@nuxt/ui'],
  devtools: { enabled: true },
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
        { name: 'color-scheme', content: 'dark' }
      ]
    }
  },
  colorMode: {
    preference: 'dark',
    fallback: 'dark'
  },
  css: ['~/assets/css/main.css'],
  typescript: { strict: true }
})
