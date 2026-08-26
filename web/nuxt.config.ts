export default defineNuxtConfig({
  ssr: false,
  devtools: { enabled: true },
  app: {
    head: {
      title: 'llamacpp-manager',
      meta: [
        { name: 'viewport', content: 'width=device-width, initial-scale=1' },
        { name: 'color-scheme', content: 'dark light' }
      ]
    }
  },
  typescript: { strict: true },
  nitro: { preset: 'static' }
})
