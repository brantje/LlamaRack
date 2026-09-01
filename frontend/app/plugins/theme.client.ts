export default defineNuxtPlugin(() => {
  const { initializeTheme } = useAppTheme()
  initializeTheme()
})
