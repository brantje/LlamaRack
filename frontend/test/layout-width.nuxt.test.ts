import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

describe('default layout content width', () => {
  it('keeps responsive page gutters without constraining the dashboard body width', () => {
    const layout = readFileSync(resolve(process.cwd(), 'app/layouts/default.vue'), 'utf8')
    const body = layout.match(/<template #body>([\s\S]*?)<\/template>/)?.[1] ?? ''

    expect(body).toContain('class="w-full p-4 sm:p-6 lg:p-10"')
    expect(body).not.toContain('max-w-')
    expect(body).not.toContain('mx-auto')
  })
})
