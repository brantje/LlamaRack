import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const source = readFileSync(resolve(process.cwd(), 'app/pages/models/new.vue'), 'utf8')

describe('Add model responsive header', () => {
  it('stacks Back below the intro before narrow viewports can squeeze the copy', () => {
    expect(source).toContain('data-testid="model-add-header"')
    expect(source).toContain('flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between')
    expect(source).toContain('class="w-full min-w-0 sm:flex-1"')
    expect(source).toContain('w-full sm:w-auto sm:shrink-0')
  })
})
