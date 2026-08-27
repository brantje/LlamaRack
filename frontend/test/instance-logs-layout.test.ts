import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const source = readFileSync(fileURLToPath(new URL('../app/pages/instances/index.vue', import.meta.url)), 'utf8')

describe('Instance logs layout', () => {
  it('keeps the logs dialog console-sized instead of using the narrow modal default', () => {
    expect(source).toContain("content: 'w-[calc(100vw-2rem)] max-w-none sm:max-w-6xl'")
    expect(source).toContain('data-testid="instance-logs-output"')
    expect(source).toContain('min-h-[55vh] max-h-[75vh]')
  })
})
