import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const source = readFileSync(resolve(process.cwd(), 'app/pages/playground.vue'), 'utf8')

describe('Playground redesign cleanup', () => {
  it('uses the shared semantic request error note', () => {
    expect(source).toContain('data-testid="playground-error"')
    expect(source).toContain('<StatusTag variant="failed">Request error</StatusTag>')
    expect(source).not.toContain('bg-[var(--accent-100)] px-3 py-2 text-xs text-[var(--accent-900)]')
  })

  it('uses mono tabular typography for numeric parameters and diagnostics', () => {
    expect(source.match(/class="mt-1 font-mono tabular-nums"/g)).toHaveLength(7)
    expect(source).toContain('message.stats" class="mt-2 font-mono text-[11px] tabular-nums')
    expect(source).toContain('Prompt tokens</dt><dd class="font-mono tabular-nums"')
    expect(source).toContain('Tokens / second</dt><dd class="font-mono tabular-nums"')
  })

  it('keeps mobile experiment controls next to the composer without duplicating the desktop Instance list', () => {
    expect(source).toContain('data-testid="playground-mobile-controls"')
    expect(source).toContain('aria-label="Playground Instance"')
    expect(source).toContain('data-testid="playground-mobile-parameters-toggle"')
    expect(source).toContain('data-testid="playground-mobile-quick-parameters"')
    expect(source).toContain('Quick controls stay beside the composer on mobile.')
    expect(source).toContain('hidden border-b border-[var(--color-divider)] p-4 xl:block')
    expect(source).toContain('text-[16px] font-semibold')
    expect(source).toContain('text-[var(--color-on-accent)]')
    expect(source).not.toContain('bg-[var(--color-accent)] text-white')
  })

  it('uses the signed-in management bridge without a Playground API-key field', () => {
    expect(source).toContain("import { readManagementToken } from '~/composables/useManagerApi'")
    expect(source).toContain('/api/v1/playground/chat/completions')
    expect(source).not.toContain('playground-api-key')
    expect(source).not.toContain('lcm-playground-api-key')
    expect(source).not.toContain("const apiKey = ref('')")
  })
})
