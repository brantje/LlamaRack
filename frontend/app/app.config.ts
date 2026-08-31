export default defineAppConfig({
  icon: {
    customize: (content: string, _name: string, prefix: string, _provider: string) => {
      if (prefix !== 'lucide') return content
      return content.replace(/stroke-width="[^"]*"/g, 'stroke-width="1.5"')
    }
  },
  ui: {
    colors: {
      primary: 'accent',
      secondary: 'neutral',
      success: 'accent',
      info: 'accent',
      warning: 'neutral',
      error: 'danger',
      neutral: 'neutral'
    },
    button: {
      slots: {
        base: 'cursor-pointer font-semibold rounded-none max-lg:min-h-11 max-lg:min-w-11'
      }
    },
    select: {
      slots: {
        base: 'rounded-none max-lg:min-h-11'
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
        root: 'w-full basis-full border-0 py-0 sm:basis-auto',
        headline: 'text-xs uppercase tracking-[.1em] text-muted',
        title: 'font-[var(--font-heading)] text-[30px] font-semibold tracking-[-.015em] text-highlighted',
        description: 'text-[15px] leading-[1.55] text-muted'
      }
    },
    dashboardGroup: {
      base: () => 'fixed inset-0 flex overflow-auto'
    }
  }
})
