import type { Page } from '@playwright/test'

const dashboardPanelSelector = '#dashboard-panel-manager-main'

export async function prepareFullPageScreenshot(page: Page) {
  const panel = page.locator(dashboardPanelSelector)

  await panel.evaluate((element) => {
    const panelElement = element as HTMLElement
    const layoutChain: HTMLElement[] = []

    for (let current: HTMLElement | null = panelElement; current; current = current.parentElement) {
      layoutChain.push(current)
      if (current === document.body) break
    }

    for (const current of layoutChain) {
      current.style.setProperty('height', 'auto', 'important')
      current.style.setProperty('max-height', 'none', 'important')
      current.style.setProperty('overflow-y', 'visible', 'important')

      if (getComputedStyle(current).position === 'fixed') {
        current.style.setProperty('position', 'static', 'important')
        current.style.setProperty('inset', 'auto', 'important')
      }
    }

    document.documentElement.style.setProperty('height', 'auto', 'important')
    document.documentElement.style.setProperty('max-height', 'none', 'important')
    document.documentElement.style.setProperty('overflow-y', 'visible', 'important')
  })

  await page.evaluate(() => new Promise<void>((resolve) => {
    requestAnimationFrame(() => requestAnimationFrame(() => resolve()))
  }))
}
