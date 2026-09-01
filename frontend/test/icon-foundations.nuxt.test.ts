import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

describe('icon design foundation', () => {
  it('normalizes only Lucide icons to the redesign 1.5 stroke width', () => {
    const source = readFileSync(resolve(process.cwd(), 'app/app.config.ts'), 'utf8')
    expect(source).toContain("prefix !== 'lucide'")
    expect(source).toContain("stroke-width=\"1.5\"")
    expect(source).toContain("content.replace(/stroke-width=\"[^\"]*\"/g")
  })
})
