export default defineAppConfig({
  ui: {
    colors: {
      primary: 'accent',
      secondary: 'neutral',
      success: 'accent',
      info: 'accent',
      warning: 'accent',
      error: 'accent',
      neutral: 'neutral'
    },
    button: {
      slots: {
        base: 'cursor-pointer font-semibold rounded-none'
      }
    },
    badge: {
      slots: {
        base: 'rounded-none'
      }
    },
    card: {
      slots: {
        root: 'rounded-none border border-default bg-elevated shadow-none'
      }
    },
    pageHeader: {
      slots: {
        root: 'border-0 py-0',
        headline: 'text-[10px] uppercase tracking-[.1em] text-muted',
        title: 'font-[var(--font-heading)] text-[30px] font-semibold tracking-[-.015em] text-highlighted',
        description: 'text-[15px] leading-[1.55] text-muted'
      }
    }
  }
})
