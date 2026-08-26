export default defineNuxtConfig({
  devtools: { enabled: true },
  runtimeConfig: {
    public: {
      apiBase: process.env.NUXT_PUBLIC_API_BASE || 'http://localhost:8888'
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
  css: ['~/assets/css/main.css'],
  typescript: { strict: true }
})
