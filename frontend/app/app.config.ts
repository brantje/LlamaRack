export default defineAppConfig({
  ui: {
    colors: {
      primary: 'mint',
      secondary: 'purple',
      success: 'mint',
      info: 'sky',
      warning: 'warn',
      error: 'danger',
      neutral: 'slate'
    },
    button: {
      slots: {
        base: 'font-semibold'
      }
    },
    card: {
      slots: {
        root: 'bg-elevated/90 border-default'
      }
    },
    pageHeader: {
      slots: {
        root: 'border-0 py-0',
        headline: 'text-[11px] font-extrabold tracking-[0.18em] text-muted',
        title: 'text-3xl sm:text-[34px] font-bold text-highlighted',
        description: 'text-sm sm:text-base text-muted'
      }
    }
  }
})
