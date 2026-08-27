import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const alwaysOnCopy = 'Keep this Instance running whenever resources permit.'
const evictionCopy = 'Allow the manager to stop this Instance when RAM/VRAM is needed for another Instance.'

function source(path: string) {
  return readFileSync(resolve(process.cwd(), path), 'utf8')
}

describe('Instance lifecycle policy copy', () => {
  it.each([
    'app/pages/instances/new.vue',
    'app/pages/instances/[id]/edit.vue',
    'app/pages/models/new.vue'
  ])('keeps Always On and eviction protection visibly independent on %s', (path) => {
    const page = source(path)
    expect(page).toContain('label="Always On"')
    expect(page).toContain(alwaysOnCopy)
    expect(page).toContain('label="Allow resource-pressure eviction"')
    expect(page).toContain(evictionCopy)
  })
})
