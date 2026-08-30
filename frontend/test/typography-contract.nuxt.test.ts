import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

function source(path: string) {
  return readFileSync(resolve(process.cwd(), path), 'utf8')
}

describe('redesign typography contract', () => {
  it('defines the complete shared type scale from the foundations spec', () => {
    const shared = source('app/themes/shared.css')
    const expected = [
      '--font-size-body: 15px',
      '--line-height-body: 1.55',
      '--font-size-h1: 42px',
      '--line-height-h1: 1.05',
      '--font-size-screen-title: 30px',
      '--font-size-h2: 32px',
      '--line-height-h2: 1.1',
      '--font-size-h3: 25px',
      '--line-height-h3: 1.15',
      '--font-size-h4: 20px',
      '--line-height-h4: 1.2',
      '--font-size-h5: 16px',
      '--line-height-h5: 1.25',
      '--font-size-h6: 13px',
      '--line-height-h6: 1.3',
      '--font-size-nav: 13.5px',
      '--font-size-table-body: 13.5px',
      '--font-size-table-header: 11px',
      '--font-size-kicker: 10px'
    ]
    for (const token of expected) expect(shared).toContain(token)
  })

  it('consumes shared type tokens instead of duplicating the scale in application CSS', () => {
    const main = source('app/assets/css/main.css')
    for (const token of [
      '--font-size-body', '--line-height-body', '--font-size-h1', '--line-height-h1',
      '--font-size-h2', '--line-height-h2', '--font-size-h3', '--line-height-h3',
      '--font-size-h4', '--line-height-h4', '--font-size-h5', '--line-height-h5',
      '--font-size-h6', '--line-height-h6', '--font-size-nav', '--font-size-table-body', '--font-size-table-header'
    ]) expect(main).toContain(`var(${token})`)

    expect(main.indexOf("@import url('https://fonts.googleapis.com")).toBe(0)
    expect(main).not.toContain('h1 { font-size: 42px')
    expect(main).not.toContain('font-size: 13.5px')
    expect(main).not.toContain('font-size: 11px')
  })
})
