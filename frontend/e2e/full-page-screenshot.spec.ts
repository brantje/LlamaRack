import { expect, test } from '@playwright/test'
import { prepareFullPageScreenshot } from './full-page-screenshot'

test('expands a fixed dashboard layout before full-page capture', async ({ page }) => {
  await page.setContent(`
    <!DOCTYPE html>
    <html>
      <head>
        <meta name="viewport" content="width=device-width, initial-scale=1">
        <style>
          html, body { margin: 0; height: 100%; overflow: hidden; }
        </style>
      </head>
      <body>
        <div style="position: fixed; inset: 0; overflow-y: hidden;">
          <main id="dashboard-panel-manager-main" style="height: 100%; overflow-y: auto;">
            <div style="height: 2500px;">Long dashboard content</div>
          </main>
        </div>
      </body>
    </html>
  `)

  const viewportHeight = page.viewportSize()?.height ?? 0
  const beforeHeight = await page.evaluate(() => document.documentElement.scrollHeight)
  expect(beforeHeight).toBeLessThanOrEqual(viewportHeight)

  await prepareFullPageScreenshot(page)

  const afterHeight = await page.evaluate(() => document.documentElement.scrollHeight)
  expect(afterHeight).toBeGreaterThan(viewportHeight)

  const screenshot = await page.screenshot({ fullPage: true, animations: 'disabled' })
  expect(screenshot.readUInt32BE(20)).toBeGreaterThan(viewportHeight)
})
